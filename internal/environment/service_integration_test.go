package environment

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	credentialservice "github.com/smartx/sks-migration-center/internal/credential"
	"github.com/smartx/sks-migration-center/internal/database"
	domainenvironment "github.com/smartx/sks-migration-center/internal/domain/environment"
	postgresrepository "github.com/smartx/sks-migration-center/internal/repository/postgres"
	"github.com/smartx/sks-migration-center/internal/security"
)

func TestEnvironmentCredentialPersistenceIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{URL: databaseURL, MaxConnections: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	credentialRepository := postgresrepository.NewCredentialRepository(pool)
	keyring, _ := security.NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{7}, 32)})
	vault, _ := credentialservice.NewVault(credentialRepository, keyring)
	kubernetesClient := &fakeKubernetesClient{probeResult: successfulProbe()}
	service, _ := NewService(postgresrepository.NewEnvironmentRepository(pool), vault, kubernetesClient)
	plaintext := []byte("apiVersion: v1\nsecret-token: integration-plaintext")
	value, err := service.Create(ctx, CreateInput{
		Name: "environment-" + uuid.NewString(), Role: domainenvironment.RoleTarget,
		Kind: domainenvironment.KindKubernetes, Credential: plaintext,
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), value.ID) })

	var persisted []byte
	if err := pool.QueryRow(ctx, "SELECT encrypted_payload FROM credentials WHERE id=$1", *value.CredentialID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted credential: %v", err)
	}
	if bytes.Contains(persisted, plaintext) || bytes.Contains(persisted, []byte("integration-plaintext")) {
		t.Fatal("kubeconfig was persisted in plaintext")
	}
	connection, err := service.TestConnection(ctx, value.ID)
	if err != nil || !connection.Success {
		t.Fatalf("test encrypted credential: result=%+v err=%v", connection, err)
	}
	capabilities, err := service.RefreshCapabilities(ctx, value.ID)
	if err != nil || len(capabilities.StorageClasses) != 1 || capabilities.CSIDrivers[0] != "smtx-elf-csi-driver" {
		t.Fatalf("persist full capabilities: value=%+v err=%v", capabilities, err)
	}
	var storedVersion string
	if err := pool.QueryRow(ctx, "SELECT capabilities->>'kubernetesVersion' FROM environments WHERE id=$1", value.ID).Scan(&storedVersion); err != nil || storedVersion != "v1.36.2" {
		t.Fatalf("stored capabilities version=%q err=%v", storedVersion, err)
	}
	if err := service.Delete(ctx, value.ID); err != nil {
		t.Fatalf("delete environment: %v", err)
	}
	var credentialCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM credentials WHERE id=$1", *value.CredentialID).Scan(&credentialCount); err != nil || credentialCount != 0 {
		t.Fatalf("credential cleanup count=%d err=%v", credentialCount, err)
	}
}

func successfulProbe() kubernetesadapter.ProbeResult {
	return kubernetesadapter.ProbeResult{
		Endpoint: "https://sks.example.test:6443",
		Capabilities: domainenvironment.Capabilities{
			KubernetesVersion: "v1.36.2", NodeCount: 3, NamespaceCount: 8, Architectures: []string{"amd64"},
			StorageClasses: []domainenvironment.StorageClass{{Name: "smtx-block", Provisioner: "smtx-elf-csi-driver", Default: true}},
			CSIDrivers:     []string{"smtx-elf-csi-driver"}, VolumeSnapshotClasses: []string{"smtx-snapshot"},
			IngressClasses: []string{"nginx"}, APIGroups: []string{"apps", "v1"}, Allocatable: map[string]string{"cpu": "48"},
		},
		Checks: []domainenvironment.ConnectionCheck{{Name: "API Server", Status: domainenvironment.CheckPassed, Message: "已连接"}},
	}
}
