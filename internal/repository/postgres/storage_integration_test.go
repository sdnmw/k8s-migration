package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
)

func TestStorageRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, database.Config{URL: databaseURL, MaxConnections: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now, environmentID := time.Now().UTC().Truncate(time.Microsecond), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO environments(id,name,role,kind,status,created_at,updated_at)
		VALUES($1,$2,'TARGET','KUBERNETES','CONNECTED',$3,$3)`, environmentID, "storage-"+uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id=$1", environmentID) })
	repository := NewStorageRepository(pool)
	value := storage.Profile{
		ID: uuid.New(), EnvironmentID: environmentID, Name: "nfs-" + uuid.NewString(), Type: storage.ProfileExternalNFS,
		StorageClassName: "nfs-" + uuid.NewString(), Provisioner: "nfs.csi.k8s.io", NFSServer: "10.0.0.20",
		NFSExport: "/migration", MountOptions: []string{"nfsvers=4.1", "hard"}, ReclaimPolicy: storage.ReclaimRetain,
		Status: storage.StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateStorageProfile(ctx, value); err != nil {
		t.Fatal(err)
	}
	created, err := repository.GetStorageProfile(ctx, value.ID)
	if err != nil || len(created.MountOptions) != 2 || created.NFSServer != value.NFSServer {
		t.Fatalf("get storage profile = %+v, %v", created, err)
	}
	created.Status, created.UpdatedAt = storage.StatusReady, now.Add(time.Second)
	if err := repository.UpdateStorageProfile(ctx, created); err != nil {
		t.Fatal(err)
	}
	values, err := repository.ListStorageProfiles(ctx, &environmentID)
	if err != nil || len(values) != 1 || values[0].Status != storage.StatusReady {
		t.Fatalf("list storage profiles = %+v, %v", values, err)
	}
}
