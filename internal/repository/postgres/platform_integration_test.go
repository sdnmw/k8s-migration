package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
)

func TestPlatformRepositoryIntegration(t *testing.T) {
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

	now := time.Now().UTC().Truncate(time.Microsecond)
	credentialID, environmentID := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO credentials(id,name,type,encrypted_payload,key_version,created_at,updated_at)
		VALUES($1,$2,'S3',$3,1,$4,$4)`, credentialID, "platform-"+uuid.NewString(), []byte("encrypted"), now)
	if err != nil {
		t.Fatalf("insert credential fixture: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO environments(id,name,role,kind,status,created_at,updated_at)
		VALUES($1,$2,'TARGET','KUBERNETES','CONNECTED',$3,$3)`, environmentID, "platform-"+uuid.NewString(), now)
	if err != nil {
		t.Fatalf("insert environment fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id=$1", environmentID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM credentials WHERE id=$1", credentialID)
	})

	repository := NewPlatformRepository(pool)
	profile := platform.ObjectStorageProfile{
		ID: uuid.New(), Name: "minio-" + uuid.NewString(), Endpoint: "https://minio.example.test",
		Bucket: "velero", Region: "minio", CredentialID: credentialID, TLSVerify: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateObjectStorageProfile(ctx, profile); err != nil {
		t.Fatalf("create object storage profile: %v", err)
	}
	created, err := repository.GetObjectStorageProfile(ctx, profile.ID)
	if err != nil || created.Endpoint != profile.Endpoint || created.CredentialID != credentialID {
		t.Fatalf("get object storage profile = %+v, %v", created, err)
	}
	profiles, err := repository.ListObjectStorageProfiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("list object storage profiles = %d, %v", len(profiles), err)
	}

	installation := platform.AddonInstallation{
		ID: uuid.New(), EnvironmentID: environmentID, Type: platform.AddonMinIO, Version: "SAFE.TEST",
		Status: platform.InstallationInstalling, Values: map[string]any{"storageClass": "smtx-block"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.UpsertAddonInstallation(ctx, installation); err != nil {
		t.Fatalf("create add-on installation: %v", err)
	}
	installation.Status = platform.InstallationReady
	installation.Message = "verified"
	installation.UpdatedAt = now.Add(time.Second)
	if err := repository.UpsertAddonInstallation(ctx, installation); err != nil {
		t.Fatalf("update add-on installation: %v", err)
	}
	stored, err := repository.GetAddonInstallation(ctx, environmentID, platform.AddonMinIO)
	if err != nil || stored.ID != installation.ID || stored.Status != platform.InstallationReady || stored.Values["storageClass"] != "smtx-block" {
		t.Fatalf("get add-on installation = %+v, %v", stored, err)
	}
	installations, err := repository.ListAddonInstallations(ctx, environmentID)
	if err != nil || len(installations) != 1 {
		t.Fatalf("list add-on installations = %d, %v", len(installations), err)
	}
}
