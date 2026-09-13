package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
)

func TestMappingRepositoryCRUDIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{URL: url, MaxConnections: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	targetID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := NewEnvironmentRepository(pool).Create(ctx, environment.Environment{ID: targetID, Name: "mapping-" + uuid.NewString(), Role: environment.RoleTarget, Kind: environment.KindKubernetes, Status: environment.StatusConnected, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id=$1", targetID) })
	repo := NewMappingRepository(pool)
	id := uuid.New()
	value := mapping.Profile{ID: id, Name: "default", TargetEnvironmentID: targetID, Storage: []mapping.KeyValue{{Source: "old", Target: "smtx-block"}}, NFS: []mapping.NFSMapping{{SourceServer: "source", SourceExport: "/data", TargetServer: "target", TargetExport: "/data", TargetStorageClass: "nfs-rwx"}}, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, value); err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, err := repo.Get(ctx, id)
	if err != nil || len(stored.NFS) != 1 || len(stored.Storage) != 1 {
		t.Fatalf("get=%+v err=%v", stored, err)
	}
	stored.Name = "updated"
	stored.UpdatedAt = now.Add(time.Minute)
	if err := repo.Update(ctx, stored); err != nil {
		t.Fatalf("update: %v", err)
	}
	values, err := repo.List(ctx, &targetID)
	if err != nil || len(values) != 1 || values[0].Name != "updated" {
		t.Fatalf("list=%+v err=%v", values, err)
	}
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
