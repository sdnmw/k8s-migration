package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestAssessmentRepositoryCreateAndGetIntegration(t *testing.T) {
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
	applicationID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := NewEnvironmentRepository(pool).Create(ctx, environment.Environment{ID: environmentID, Name: "assessment-" + uuid.NewString(), Role: environment.RoleSource, Kind: environment.KindKubernetes, Status: environment.StatusConnected, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	if _, err := NewApplicationRepository(pool).Upsert(ctx, application.SourceApplication{ID: applicationID, EnvironmentID: environmentID, Name: "shop", Namespace: "shop", SourceType: application.SourceKubernetes, Inventory: application.Inventory{Counts: map[string]int{}}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create application: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id=$1", environmentID) })

	completedAt := now.Add(time.Second)
	assessmentID := uuid.New()
	value := assessment.Assessment{ID: assessmentID, ApplicationID: applicationID, Score: 60, BlockerCount: 1, WarningCount: 1, Status: assessment.StatusCompleted, CreatedAt: now, CompletedAt: &completedAt, Issues: []assessment.Issue{
		{ID: uuid.New(), AssessmentID: assessmentID, Severity: assessment.SeverityWarning, Category: assessment.CategoryCompute, ResourceKind: "Deployment", ResourceNamespace: "shop", ResourceName: "api", RuleID: "COMPUTE_RESOURCE_REQUESTS", Title: "missing requests", Description: "cpu and memory", Remediation: "set requests"},
		{ID: uuid.New(), AssessmentID: assessmentID, Severity: assessment.SeverityBlocker, Category: assessment.CategoryAPI, ResourceKind: "Ingress", ResourceNamespace: "shop", ResourceName: "web", RuleID: "API_REMOVED_VERSION", Title: "removed API", Description: "upgrade", AutoFixable: true},
	}}
	repository := NewAssessmentRepository(pool)
	if err := repository.Create(ctx, value); err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	stored, err := repository.Get(ctx, assessmentID)
	if err != nil {
		t.Fatalf("get assessment: %v", err)
	}
	if stored.Score != 60 || stored.BlockerCount != 1 || stored.WarningCount != 1 || len(stored.Issues) != 2 || stored.Issues[0].Severity != assessment.SeverityBlocker {
		t.Fatalf("unexpected assessment: %+v", stored)
	}
}
