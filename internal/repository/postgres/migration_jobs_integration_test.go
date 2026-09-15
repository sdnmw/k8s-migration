package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/database"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
	baserepository "github.com/smartx/sks-migration-center/internal/repository"
)

func TestMigrationStateAndJobLeaseIntegration(t *testing.T) {
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

	fixture := seedMigrationPlan(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM migration_plans WHERE id=$1", fixture.planID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id IN ($1,$2)", fixture.sourceEnvironmentID, fixture.targetEnvironmentID)
	})

	migrations := NewMigrationRepository(pool)
	jobs := NewJobRepository(pool)
	secondPlan := migration.Plan{
		ID: uuid.New(), Name: "repository-plan-" + uuid.NewString(),
		SourceEnvironmentID: fixture.sourceEnvironmentID, TargetEnvironmentID: fixture.targetEnvironmentID,
		SourceApplicationID: fixture.applicationID, AssessmentID: fixture.assessmentID, MappingProfileID: &fixture.mappingID,
		Strategy:         migration.Strategy{ResourceMode: "TRANSFORM", VolumeMode: migration.VolumeFSBackup, PreSyncEnabled: true},
		ValidationPolicy: migration.ValidationPolicy{RequireWorkloadsReady: true, RequirePVCsBound: true, TimeoutSeconds: 300},
		Status:           migration.PlanDraft, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := migrations.CreatePlan(ctx, secondPlan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM migration_plans WHERE id=$1", secondPlan.ID)
	})
	storedPlan, err := migrations.GetPlan(ctx, secondPlan.ID)
	if err != nil || storedPlan.Strategy.VolumeMode != migration.VolumeFSBackup || storedPlan.ValidationPolicy.TimeoutSeconds != 300 {
		t.Fatalf("stored plan = %+v, %v", storedPlan, err)
	}
	if err := migrations.UpdatePlanStatus(ctx, secondPlan.ID, migration.PlanReady); err != nil {
		t.Fatalf("ready plan: %v", err)
	}
	plans, err := migrations.ListPlans(ctx)
	if err != nil || len(plans) < 2 {
		t.Fatalf("list plans = %d, %v", len(plans), err)
	}
	run := migration.Run{
		ID:        uuid.New(),
		PlanID:    fixture.planID,
		RunNumber: 1,
		Status:    migration.RunPending,
	}
	if err := migrations.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	runSummaries, err := migrations.ListRuns(ctx)
	if err != nil || len(runSummaries) == 0 {
		t.Fatalf("list run summaries = %+v, %v", runSummaries, err)
	}
	foundSummary := false
	for _, summary := range runSummaries {
		if summary.ID == run.ID {
			foundSummary = summary.PlanName != "" && summary.SourceEnvironmentName != "" && summary.TargetEnvironmentName != "" && summary.ApplicationName != ""
		}
	}
	if !foundSummary {
		t.Fatalf("run summary did not include joined display fields: %+v", runSummaries)
	}
	transfer := migration.VolumeTransfer{
		ID: uuid.New(), RunID: run.ID, Engine: migration.TransferVeleroFSB, Namespace: "demo",
		SourceVolume: "db-0/data", TargetVolume: "data", TotalBytes: 4096, TransferredBytes: 1024,
		ChecksumStatus: "PENDING", Status: migration.TransferRunning,
	}
	if err := migrations.UpsertVolumeTransfers(ctx, run.ID, []migration.VolumeTransfer{transfer}); err != nil {
		t.Fatalf("upsert volume transfer: %v", err)
	}
	transfer.TransferredBytes, transfer.Status = 4096, migration.TransferCompleted
	if err := migrations.UpsertVolumeTransfers(ctx, run.ID, []migration.VolumeTransfer{transfer}); err != nil {
		t.Fatalf("update volume transfer: %v", err)
	}
	if err := migrations.UpdateRunBytes(ctx, run.ID, 4096, 4096); err != nil {
		t.Fatalf("update run bytes: %v", err)
	}
	if err := migrations.AppendEvent(ctx, migration.Event{RunID: run.ID, Type: "VOLUME_TRANSFER_PROGRESS", Message: "4096/4096"}); err != nil {
		t.Fatalf("append volume event: %v", err)
	}
	transfers, err := migrations.ListVolumeTransfers(ctx, run.ID)
	if err != nil || len(transfers) != 1 || transfers[0].TransferredBytes != 4096 || transfers[0].Status != migration.TransferCompleted {
		t.Fatalf("stored transfers = %+v, %v", transfers, err)
	}
	storedBytes, err := migrations.GetRun(ctx, run.ID)
	if err != nil || storedBytes.BytesTransferred != 4096 || storedBytes.BytesTotal != 4096 {
		t.Fatalf("stored run byte progress = %+v, %v", storedBytes, err)
	}
	snapshots := []migration.WorkloadReplicaSnapshot{
		{ID: uuid.New(), RunID: run.ID, Namespace: "demo", Kind: "Deployment", Name: "api", Replicas: 3},
		{ID: uuid.New(), RunID: run.ID, Namespace: "demo", Kind: "StatefulSet", Name: "db", Replicas: 1},
	}
	if err := migrations.SaveWorkloadReplicaSnapshots(ctx, run.ID, snapshots); err != nil {
		t.Fatalf("save workload replica snapshots: %v", err)
	}
	snapshots[0].Replicas = 0
	if err := migrations.SaveWorkloadReplicaSnapshots(ctx, run.ID, snapshots[:1]); err != nil {
		t.Fatalf("repeat workload replica snapshot: %v", err)
	}
	storedSnapshots, err := migrations.ListWorkloadReplicaSnapshots(ctx, run.ID)
	if err != nil || len(storedSnapshots) != 2 || storedSnapshots[0].Replicas != 3 {
		t.Fatalf("stored workload replica snapshots = %+v, %v", storedSnapshots, err)
	}

	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	firstStep, err := migrations.EnsureStep(ctx, migration.Step{
		ID:             uuid.New(),
		RunID:          run.ID,
		Type:           migration.StepPreflight,
		Attempt:        1,
		Status:         migration.StepPending,
		IdempotencyKey: "preflight:v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("ensure step: %v", err)
	}
	duplicateStep, err := migrations.EnsureStep(ctx, migration.Step{
		ID:             uuid.New(),
		RunID:          run.ID,
		Type:           migration.StepPreflight,
		Attempt:        1,
		Status:         migration.StepPending,
		IdempotencyKey: "preflight:v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil || duplicateStep.ID != firstStep.ID {
		t.Fatalf("idempotent step = %s, %v; want %s", duplicateStep.ID, err, firstStep.ID)
	}

	jobID, err := jobs.Enqueue(ctx, run.ID, firstStep.ID, now)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	duplicateJobID, err := jobs.Enqueue(ctx, run.ID, firstStep.ID, now)
	if err != nil || duplicateJobID != jobID {
		t.Fatalf("idempotent enqueue = %s, %v; want %s", duplicateJobID, err, jobID)
	}

	lease, err := jobs.Claim(ctx, "worker-one", 80*time.Millisecond)
	if err != nil || lease == nil || lease.Attempt != 1 {
		var status migration.StepStatus
		var availableAt time.Time
		var leaseExpiresAt *time.Time
		var databaseNow time.Time
		diagnosticErr := pool.QueryRow(ctx, `SELECT s.status,j.available_at,j.lease_expires_at,now()
			FROM job_leases j JOIN migration_steps s ON s.id=j.step_id WHERE j.id=$1`, jobID).
			Scan(&status, &availableAt, &leaseExpiresAt, &databaseNow)
		t.Fatalf("first claim = %+v, %v; diagnostic status=%s availableAt=%s leaseExpiresAt=%v databaseNow=%s err=%v",
			lease, err, status, availableAt, leaseExpiresAt, databaseNow, diagnosticErr)
	}
	if other, err := jobs.Claim(ctx, "worker-two", time.Second); err != nil || other != nil {
		t.Fatalf("claim before expiry = %+v, %v; want nil", other, err)
	}
	time.Sleep(100 * time.Millisecond)
	recovered, err := jobs.Claim(ctx, "worker-two", time.Second)
	if err != nil || recovered == nil || recovered.ID != lease.ID || recovered.Attempt != 2 {
		t.Fatalf("recovered claim = %+v, %v", recovered, err)
	}
	if err := jobs.Heartbeat(ctx, lease.ID, "worker-one", time.Second); !errors.Is(err, baserepository.ErrNotFound) {
		t.Fatalf("stale worker heartbeat = %v; want ErrNotFound", err)
	}
	if err := jobs.Complete(ctx, recovered.ID, recovered.OwnerID); err != nil {
		t.Fatalf("complete recovered job: %v", err)
	}
	steps, err := migrations.ListSteps(ctx, run.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != migration.StepSucceeded || steps[0].Attempt != 2 {
		t.Fatalf("completed steps = %+v, %v", steps, err)
	}

	if err := migrations.TransitionRun(ctx, run.ID, migration.RunCompleted, migration.Event{}); err == nil {
		t.Fatal("expected illegal PREFLIGHT -> COMPLETED transition to fail")
	}
	stored, err := migrations.GetRun(ctx, run.ID)
	if err != nil || stored.Status != migration.RunPreflight {
		t.Fatalf("stored run = %+v, %v", stored, err)
	}
	if err := migrations.DeleteRun(ctx, run.ID); !errors.Is(err, baserepository.ErrConflict) {
		t.Fatalf("active run must not be archived: %v", err)
	}
	if err := migrations.TransitionRun(ctx, run.ID, migration.RunFailed, migration.Event{}); err != nil {
		t.Fatal(err)
	}
	if err := migrations.DeleteRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := migrations.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range remaining {
		if item.ID == run.ID {
			t.Fatal("archived run remains listed")
		}
	}
	preserved, err := migrations.ListWorkloadReplicaSnapshots(ctx, run.ID)
	if err != nil || len(preserved) != 2 {
		t.Fatal("archive destroyed recovery evidence")
	}
}

func TestScheduledRunProgressionCancellationAndEventResume(t *testing.T) {
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
	fixture := seedMigrationPlan(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM migration_plans WHERE id=$1", fixture.planID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM environments WHERE id IN ($1,$2)", fixture.sourceEnvironmentID, fixture.targetEnvironmentID)
	})
	migrations, jobs := NewMigrationRepository(pool), NewJobRepository(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := migration.Run{ID: uuid.New(), PlanID: fixture.planID, CreatedAt: now}
	steps := []migration.Step{
		{ID: uuid.New(), Type: migration.StepPreflight, IdempotencyKey: "preflight:v1"},
		{ID: uuid.New(), Type: migration.StepQuiesce, IdempotencyKey: "quiesce:v1"},
		{ID: uuid.New(), Type: migration.StepAwaitCutover, IdempotencyKey: "await-cutover:v1"},
	}
	stored, err := migrations.ScheduleRun(ctx, run, steps, false)
	if err != nil || stored.RunNumber != 1 || stored.Status != migration.RunPending {
		t.Fatalf("ScheduleRun = %+v, %v", stored, err)
	}
	events, err := migrations.ListEvents(ctx, run.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Type != "RUN_CREATED" {
		t.Fatalf("initial events = %+v, %v", events, err)
	}
	first, err := jobs.Claim(ctx, "worker-a", time.Second)
	if err != nil || first == nil || first.StepType != migration.StepPreflight {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if err := jobs.Complete(ctx, first.ID, first.OwnerID); err != nil {
		t.Fatalf("complete preflight: %v", err)
	}
	second, err := jobs.Claim(ctx, "worker-b", time.Second)
	if err != nil || second == nil || second.StepType != migration.StepQuiesce {
		t.Fatalf("second claim = %+v, %v", second, err)
	}
	rollingBack, err := migrations.CancelRun(ctx, run.ID)
	if err != nil || rollingBack.Status != migration.RunRollingBack {
		t.Fatalf("CancelRun after quiesce = %+v, %v", rollingBack, err)
	}
	rollback, err := jobs.Claim(ctx, "worker-c", time.Second)
	if err != nil || rollback == nil || rollback.StepType != migration.StepRollback {
		t.Fatalf("rollback claim = %+v, %v", rollback, err)
	}
	if err := jobs.Complete(ctx, rollback.ID, rollback.OwnerID); err != nil {
		t.Fatalf("complete rollback: %v", err)
	}
	finished, err := migrations.GetRun(ctx, run.ID)
	if err != nil || finished.Status != migration.RunCancelled || finished.CompletedAt == nil {
		t.Fatalf("finished rollback = %+v, %v", finished, err)
	}
	resumed, err := migrations.ListEvents(ctx, run.ID, events[0].ID, 100)
	if err != nil || len(resumed) < 5 {
		t.Fatalf("resumed events = %+v, %v", resumed, err)
	}
	for _, event := range resumed {
		if event.ID <= events[0].ID {
			t.Fatalf("event cursor regressed: %+v", event)
		}
	}
}

type migrationFixture struct {
	planID              uuid.UUID
	sourceEnvironmentID uuid.UUID
	targetEnvironmentID uuid.UUID
	applicationID       uuid.UUID
	assessmentID        uuid.UUID
	mappingID           uuid.UUID
}

func seedMigrationPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool) migrationFixture {
	t.Helper()
	sourceEnvironmentID := uuid.New()
	targetEnvironmentID := uuid.New()
	applicationID := uuid.New()
	assessmentID := uuid.New()
	mappingID := uuid.New()
	planID := uuid.New()
	suffix := uuid.NewString()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO environments (id,name,role,kind,status)
		VALUES ($1,$2,'SOURCE','KUBERNETES','CONNECTED'),($3,$4,'TARGET','KUBERNETES','CONNECTED')`,
		sourceEnvironmentID, "source-"+suffix, targetEnvironmentID, "target-"+suffix); err != nil {
		t.Fatalf("insert fixture environments: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO source_applications
		(id,environment_id,name,source_type,namespace) VALUES ($1,$2,$3,'KUBERNETES','demo')`,
		applicationID, sourceEnvironmentID, "app-"+suffix); err != nil {
		t.Fatalf("insert fixture application: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assessments
		(id,application_id,score,status) VALUES ($1,$2,100,'COMPLETED')`, assessmentID, applicationID); err != nil {
		t.Fatalf("insert fixture assessment: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mapping_profiles
		(id,name,target_environment_id) VALUES ($1,$2,$3)`, mappingID, "mapping-"+suffix, targetEnvironmentID); err != nil {
		t.Fatalf("insert fixture mapping: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_plans
		(id,name,source_environment_id,target_environment_id,source_application_id,assessment_id,
		 mapping_profile_id,strategy,validation_policy,status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'{}'::jsonb,'{}'::jsonb,'READY')`, planID, "plan-"+suffix,
		sourceEnvironmentID, targetEnvironmentID, applicationID, assessmentID, mappingID); err != nil {
		t.Fatalf("insert fixture plan: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit fixture transaction: %v", err)
	}
	return migrationFixture{
		planID:              planID,
		sourceEnvironmentID: sourceEnvironmentID,
		targetEnvironmentID: targetEnvironmentID,
		applicationID:       applicationID,
		assessmentID:        assessmentID,
		mappingID:           mappingID,
	}
}
