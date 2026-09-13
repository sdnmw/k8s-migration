package migration_test

import (
	"context"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	kube "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
	"github.com/smartx/sks-migration-center/internal/credential"
	migration "github.com/smartx/sks-migration-center/internal/migration"
	postgres "github.com/smartx/sks-migration-center/internal/repository/postgres"
	"github.com/smartx/sks-migration-center/internal/security"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Explicit maintenance operation; uses the same scheduling and evidence services
// as the API, never edits run status or fabricates a browser session.
func TestLiveRetryHarbor(t *testing.T) {
	if os.Getenv("LIVE_RETRY_HARBOR") != "1" {
		t.Skip("explicit maintenance only")
	}
	root := os.Getenv("MIGRATION_WORKSPACE")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, e := exec.CommandContext(ctx, "kubectl", "--kubeconfig", root+"/.data/kubeconfigs/mw.yaml", "-n", "sks-migration-center", "get", "secret", "sks-migration-center-secrets", "-o", "jsonpath={.data.database-url}").Output()
	if e != nil {
		t.Fatal("cannot load database connection")
	}
	url, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal("invalid connection encoding")
	}
	config, e := pgxpool.ParseConfig(string(url))
	clear(url)
	if e != nil {
		t.Fatal("invalid connection config")
	}
	config.ConnConfig.Host = "127.0.0.1"
	config.ConnConfig.Port = 55439
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer pool.Close()
	repo := postgres.NewMigrationRepository(pool)
	key, e := security.LoadKeyringFile(root+"/.data/secrets/master-key", 1)
	if e != nil {
		t.Fatal(e)
	}
	vault, e := credential.NewVault(postgres.NewCredentialRepository(pool), key)
	if e != nil {
		t.Fatal(e)
	}
	evidence, e := migration.NewEvidenceService(repo, repo, repo, repo, postgres.NewApplicationRepository(pool), postgres.NewMappingRepository(pool), postgres.NewEnvironmentRepository(pool), vault, kube.NewClient(20*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	service, e := migration.NewRunService(repo, repo, repo)
	if e != nil {
		t.Fatal(e)
	}
	service.ConfigureEvidence(evidence)
	runID := os.Getenv("LIVE_RETRY_RUN_ID")
	if runID == "" {
		runID = "1a173b88-f24b-4a61-b138-6d39230a33ad"
	}
	if os.Getenv("LIVE_RUN_ACTION") == "cancel" {
		result, cancelErr := service.Cancel(ctx, uuid.MustParse(runID))
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		t.Log("requested cancellation", result.Run.ID)
		return
	}
	result, e := service.Retry(ctx, uuid.MustParse(runID))
	if e != nil {
		t.Fatal(e)
	}
	t.Log("scheduled Harbor retry", result.Run.ID)
}
