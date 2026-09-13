package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type memoryIdentityRepository struct {
	administrator *identity.Administrator
	sessions      map[string]identity.Session
	audits        []identity.AuditEvent
}

func newMemoryIdentityRepository() *memoryIdentityRepository {
	return &memoryIdentityRepository{sessions: map[string]identity.Session{}}
}

func (r *memoryIdentityRepository) AdministratorCount(context.Context) (int, error) {
	if r.administrator == nil {
		return 0, nil
	}
	return 1, nil
}

func (r *memoryIdentityRepository) CreateAdministrator(_ context.Context, value identity.Administrator) error {
	if r.administrator != nil {
		return repository.ErrConflict
	}
	r.administrator = &value
	return nil
}

func (r *memoryIdentityRepository) ChangeAdministratorPassword(_ context.Context, administratorID uuid.UUID, passwordHash string, updatedAt time.Time) error {
	if r.administrator == nil || r.administrator.ID != administratorID {
		return repository.ErrNotFound
	}
	r.administrator.PasswordHash = passwordHash
	r.administrator.UpdatedAt = updatedAt
	for tokenHash, session := range r.sessions {
		if session.AdministratorID == administratorID {
			delete(r.sessions, tokenHash)
		}
	}
	return nil
}

func (r *memoryIdentityRepository) FindAdministratorByUsername(_ context.Context, username string) (identity.Administrator, error) {
	if r.administrator == nil || r.administrator.Username != username {
		return identity.Administrator{}, repository.ErrNotFound
	}
	return *r.administrator, nil
}

func (r *memoryIdentityRepository) CreateSession(_ context.Context, value identity.Session) error {
	r.sessions[string(value.TokenHash)] = value
	return nil
}

func (r *memoryIdentityRepository) FindPrincipalByTokenHash(_ context.Context, hash []byte) (identity.Principal, error) {
	session, ok := r.sessions[string(hash)]
	if !ok || r.administrator == nil || !session.ExpiresAt.After(time.Now()) {
		return identity.Principal{}, repository.ErrNotFound
	}
	return identity.Principal{
		Administrator: *r.administrator,
		SessionID:     session.ID,
		CSRFHash:      session.CSRFHash,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}

func (r *memoryIdentityRepository) DeleteSessionByTokenHash(_ context.Context, hash []byte) error {
	delete(r.sessions, string(hash))
	return nil
}

func (r *memoryIdentityRepository) DeleteExpiredSessions(context.Context) (int64, error) {
	return 0, nil
}

func (r *memoryIdentityRepository) RecordAudit(_ context.Context, event identity.AuditEvent) error {
	r.audits = append(r.audits, event)
	return nil
}

func TestLoginSessionCSRFAndLogout(t *testing.T) {
	store := newMemoryIdentityRepository()
	service, err := NewService(store, time.Hour)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := service.EnsureAdministrator(context.Background(), "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("EnsureAdministrator: %v", err)
	}
	result, err := service.Login(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Administrator.PasswordHash != "" || result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatalf("login exposed hash or omitted tokens: %+v", result)
	}
	principal, err := service.Authenticate(context.Background(), result.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := service.ValidateCSRF(principal, result.CSRFToken, result.CSRFToken); err != nil {
		t.Fatalf("ValidateCSRF: %v", err)
	}
	if err := service.ValidateCSRF(principal, result.CSRFToken, "wrong"); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("invalid CSRF = %v", err)
	}
	if err := service.Logout(context.Background(), principal, result.SessionToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), result.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("authentication after logout = %v", err)
	}
}

func TestInvalidLoginIsGenericAndAudited(t *testing.T) {
	store := newMemoryIdentityRepository()
	service, _ := NewService(store, time.Hour)
	if err := service.EnsureAdministrator(context.Background(), "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	for _, username := range []string{"admin", "missing"} {
		if _, err := service.Login(context.Background(), username, "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login for %s = %v", username, err)
		}
	}
	if len(store.audits) != 2 || store.audits[0].Result != "FAILURE" || store.audits[1].Result != "FAILURE" {
		t.Fatalf("unexpected audits: %+v", store.audits)
	}
}

func TestChangePasswordInvalidatesSessionsAndCredentials(t *testing.T) {
	store := newMemoryIdentityRepository()
	service, err := NewService(store, time.Hour)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	oldPassword := "correct horse battery staple"
	newPassword := "new correct horse battery staple"
	if err := service.EnsureAdministrator(context.Background(), "admin", oldPassword); err != nil {
		t.Fatalf("EnsureAdministrator: %v", err)
	}
	login, err := service.Login(context.Background(), "admin", oldPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	principal, err := service.Authenticate(context.Background(), login.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := service.ChangePassword(context.Background(), principal, "incorrect password", newPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password = %v", err)
	}
	if err := service.ChangePassword(context.Background(), principal, oldPassword, "too-short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password = %v", err)
	}
	if err := service.ChangePassword(context.Background(), principal, oldPassword, oldPassword); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("unchanged password = %v", err)
	}
	if err := service.ChangePassword(context.Background(), principal, oldPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), login.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session after password change = %v", err)
	}
	if _, err := service.Login(context.Background(), "admin", oldPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login = %v", err)
	}
	if _, err := service.Login(context.Background(), "admin", newPassword); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	foundAudit := false
	for _, audit := range store.audits {
		if audit.Action == "AUTH_PASSWORD_CHANGED" && audit.Result == "SUCCESS" {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatalf("password change audit not recorded: %+v", store.audits)
	}
}
