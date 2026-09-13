package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestApplicationRepositoryUpsertIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{URL: databaseURL, MaxConnections: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	environmentID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := NewEnvironmentRepository(pool).Create(ctx, environment.Environment{ID: environmentID, Name: "inventory-" + uuid.NewString(), Role: environment.RoleSource, Kind: environment.KindKubernetes, Status: environment.StatusConnected, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id=$1", environmentID) })

	repository := NewApplicationRepository(pool)
	created, err := repository.Upsert(ctx, application.SourceApplication{ID: uuid.New(), EnvironmentID: environmentID, Name: "business", Namespace: "business", SourceType: application.SourceKubernetes, Inventory: application.Inventory{Counts: map[string]int{"Deployment": 1}}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("upsert application: %v", err)
	}
	updated, err := repository.Upsert(ctx, application.SourceApplication{ID: uuid.New(), EnvironmentID: environmentID, Name: "business", Namespace: "business", SourceType: application.SourceKubernetes, Inventory: application.Inventory{Counts: map[string]int{"Deployment": 2}}, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("update application: %v", err)
	}
	if updated.ID != created.ID || !updated.CreatedAt.Equal(created.CreatedAt) || updated.Inventory.Counts["Deployment"] != 2 {
		t.Fatalf("upsert identity or inventory changed incorrectly: created=%+v updated=%+v", created, updated)
	}
	values, err := repository.ListByEnvironment(ctx, environmentID)
	if err != nil || len(values) != 1 {
		t.Fatalf("list applications=%+v err=%v", values, err)
	}
}
