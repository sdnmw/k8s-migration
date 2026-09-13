package postgres

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/auth"
	credentialservice "github.com/smartx/sks-migration-center/internal/credential"
	"github.com/smartx/sks-migration-center/internal/database"
	domaincredential "github.com/smartx/sks-migration-center/internal/domain/credential"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	baserepository "github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/security"
)

func TestAuthenticationCredentialEncryptionAndAuditIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{
		URL: databaseURL, MaxConnections: 4, MinConnections: 0, ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	username := "security-" + uuid.NewString()
	identityRepository := NewIdentityRepository(pool)
	authentication, err := auth.NewService(identityRepository, time.Hour)
	if err != nil {
		t.Fatalf("new authentication service: %v", err)
	}
	password := "correct horse battery staple"
	if err := authentication.EnsureAdministrator(ctx, username, password); err != nil {
		t.Fatalf("bootstrap administrator: %v", err)
	}
	if err := identityRepository.CreateAdministrator(ctx, identity.Administrator{
		ID: uuid.New(), Username: "second-" + username, PasswordHash: "not-used",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); !errors.Is(err, baserepository.ErrConflict) {
		t.Fatalf("second local administrator = %v; want conflict", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM administrators WHERE username=$1", username)
		_, _ = pool.Exec(context.Background(), "DELETE FROM audit_events WHERE actor=$1", username)
	})

	login, err := authentication.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	principal, err := authentication.Authenticate(ctx, login.SessionToken)
	if err != nil || principal.Administrator.Username != username {
		t.Fatalf("authenticate = %+v, %v", principal, err)
	}
	var storedHash string
	var storedToken []byte
	if err := pool.QueryRow(ctx, `SELECT a.password_hash,s.token_hash FROM administrators a
		JOIN sessions s ON s.administrator_id=a.id WHERE a.username=$1`, username).Scan(&storedHash, &storedToken); err != nil {
		t.Fatalf("read stored authentication data: %v", err)
	}
	if !strings.HasPrefix(storedHash, "$argon2id$") || strings.Contains(storedHash, password) || bytes.Contains(storedToken, []byte(login.SessionToken)) {
		t.Fatal("password or session token was persisted without one-way protection")
	}

	credentialRepository := NewCredentialRepository(pool)
	keyring, _ := security.NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{5}, 32)})
	vault, _ := credentialservice.NewVault(credentialRepository, keyring)
	plaintext := []byte("token: kubeconfig-secret-value")
	metadata, err := vault.Store(ctx, "integration kubeconfig", domaincredential.TypeKubeconfig, plaintext)
	if err != nil {
		t.Fatalf("store credential: %v", err)
	}
	t.Cleanup(func() { _ = credentialRepository.DeleteCredential(context.Background(), metadata.ID) })
	storedCredential, err := credentialRepository.GetCredential(ctx, metadata.ID)
	if err != nil || bytes.Contains(storedCredential.EncryptedPayload, []byte("kubeconfig-secret-value")) {
		t.Fatalf("stored credential leaked plaintext: %v", err)
	}
	resolved, err := vault.Resolve(ctx, metadata.ID)
	if err != nil || !bytes.Equal(resolved, plaintext) {
		t.Fatalf("resolve credential = %q, %v", resolved, err)
	}

	if err := identityRepository.RecordAudit(ctx, identity.AuditEvent{
		Actor: username, Action: "CREDENTIAL_TEST", ObjectType: "CREDENTIAL", ObjectID: &metadata.ID,
		Result: "SUCCESS", Detail: map[string]any{"token": "must-not-persist", "endpoint": "https://cluster.example"},
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
	var detail string
	if err := pool.QueryRow(ctx, `SELECT detail::text FROM audit_events
		WHERE actor=$1 AND action='CREDENTIAL_TEST' ORDER BY id DESC LIMIT 1`, username).Scan(&detail); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(detail, "must-not-persist") || !strings.Contains(detail, security.Redacted) {
		t.Fatalf("audit detail was not redacted: %s", detail)
	}
}
