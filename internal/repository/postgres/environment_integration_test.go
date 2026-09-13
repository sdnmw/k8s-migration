package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestEnvironmentRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{
		URL:            databaseURL,
		MaxConnections: 4,
		MinConnections: 0,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	repository := NewEnvironmentRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	value := environment.Environment{
		ID:           uuid.New(),
		Name:         "integration-" + uuid.NewString(),
		Role:         environment.RoleTarget,
		Kind:         environment.KindKubernetes,
		Endpoint:     "https://sks.example.test:6443",
		Status:       environment.StatusPending,
		Capabilities: environment.Capabilities{KubernetesVersion: "v1.32.0", Architectures: []string{"amd64"}},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repository.Create(ctx, value); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	t.Cleanup(func() { _ = repository.Delete(context.Background(), value.ID) })

	created, err := repository.Get(ctx, value.ID)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	if created.Name != value.Name || created.Capabilities.KubernetesVersion != "v1.32.0" {
		t.Fatalf("unexpected environment: %+v", created)
	}

	created.Status = environment.StatusConnected
	created.UpdatedAt = now.Add(time.Second)
	if err := repository.Update(ctx, created); err != nil {
		t.Fatalf("update environment: %v", err)
	}
	values, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("list environments: %v", err)
	}
	if len(values) == 0 {
		t.Fatal("expected the created environment in list")
	}
	if err := repository.Delete(ctx, value.ID); err != nil {
		t.Fatalf("delete environment: %v", err)
	}
}
