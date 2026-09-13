package migration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type runPlanRepositoryStub struct{ plan domainmigration.Plan }

func (s *runPlanRepositoryStub) CreatePlan(context.Context, domainmigration.Plan) error { return nil }
func (s *runPlanRepositoryStub) GetPlan(context.Context, uuid.UUID) (domainmigration.Plan, error) {
	return s.plan, nil
}
func (s *runPlanRepositoryStub) ListPlans(context.Context) ([]domainmigration.Plan, error) {
	return nil, nil
}
func (s *runPlanRepositoryStub) UpdatePlanStatus(context.Context, uuid.UUID, domainmigration.PlanStatus) error {
	return nil
}

type runRepositoryStub struct {
	run       domainmigration.Run
	summaries []domainmigration.RunSummary
	steps     []domainmigration.Step
	events    []domainmigration.Event
	retry     bool
	cancelled bool
	confirmed bool
	restored  bool
}

func (s *runRepositoryStub) ScheduleRun(_ context.Context, run domainmigration.Run, steps []domainmigration.Step, retry bool) (domainmigration.Run, error) {
	run.RunNumber = 2
	s.run, s.steps, s.retry = run, steps, retry
	return run, nil
}
func (s *runRepositoryStub) GetRun(context.Context, uuid.UUID) (domainmigration.Run, error) {
	return s.run, nil
}
func (s *runRepositoryStub) ListRuns(context.Context) ([]domainmigration.RunSummary, error) {
	return append([]domainmigration.RunSummary(nil), s.summaries...), nil
}
func (s *runRepositoryStub) ListSteps(context.Context, uuid.UUID) ([]domainmigration.Step, error) {
	return s.steps, nil
}
func (s *runRepositoryStub) ListEvents(context.Context, uuid.UUID, int64, int) ([]domainmigration.Event, error) {
	return append([]domainmigration.Event(nil), s.events...), nil
}
func (s *runRepositoryStub) CancelRun(context.Context, uuid.UUID) (domainmigration.Run, error) {
	s.cancelled = true
	s.run.Status = domainmigration.RunCancelled
	return s.run, nil
}
func (s *runRepositoryStub) RestoreSource(context.Context, uuid.UUID) (domainmigration.Run, error) {
	s.restored = true
	s.run.Status = domainmigration.RunRollingBack
	return s.run, nil
}
func (s *runRepositoryStub) ConfirmCutover(context.Context, uuid.UUID, uuid.UUID, []string) error {
	s.confirmed = true
	return nil
}

func TestRunServiceSchedulesDeterministicStepChain(t *testing.T) {
	plan := domainmigration.Plan{ID: uuid.New(), Status: domainmigration.PlanReady, Strategy: domainmigration.Strategy{PreSyncEnabled: true, VolumeMode: domainmigration.VolumeFSBackup}}
	plans := &runPlanRepositoryStub{plan: plan}
	runs := &runRepositoryStub{}
	service, err := NewRunService(plans, runs)
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	service.clock = func() time.Time { return now }
	result, err := service.Start(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []domainmigration.StepType{domainmigration.StepPreflight, domainmigration.StepPreSync, domainmigration.StepQuiesce, domainmigration.StepFinalBackup, domainmigration.StepTransfer, domainmigration.StepTransform, domainmigration.StepRestore, domainmigration.StepValidation, domainmigration.StepAwaitCutover}
	if result.Run.RunNumber != 2 || len(result.Steps) != len(want) {
		t.Fatalf("snapshot = %+v", result)
	}
	for index := range want {
		if result.Steps[index].Type != want[index] || result.Steps[index].IdempotencyKey == "" {
			t.Fatalf("step %d = %+v; want %s", index, result.Steps[index], want[index])
		}
	}
}

func TestRunServiceListsTaskSummaries(t *testing.T) {
	want := domainmigration.RunSummary{Run: domainmigration.Run{ID: uuid.New()}, PlanName: "orders", SourceEnvironmentName: "sida", TargetEnvironmentName: "mw"}
	runs := &runRepositoryStub{summaries: []domainmigration.RunSummary{want}}
	service, _ := NewRunService(&runPlanRepositoryStub{}, runs)
	got, err := service.List(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != want.ID || got[0].PlanName != "orders" {
		t.Fatalf("List = %+v, %v", got, err)
	}
}

func TestRunServiceRetryRequiresTerminalFailureOrCancellation(t *testing.T) {
	plan := domainmigration.Plan{ID: uuid.New(), Status: domainmigration.PlanFailed, Strategy: domainmigration.Strategy{VolumeMode: domainmigration.VolumeNone}}
	runs := &runRepositoryStub{run: domainmigration.Run{ID: uuid.New(), PlanID: plan.ID, Status: domainmigration.RunValidation}}
	service, _ := NewRunService(&runPlanRepositoryStub{plan: plan}, runs)
	if _, err := service.Retry(context.Background(), runs.run.ID); err != ErrInvalidRunState {
		t.Fatalf("Retry error = %v", err)
	}
	runs.run.Status = domainmigration.RunFailed
	result, err := service.Retry(context.Background(), runs.run.ID)
	if err != nil || !runs.retry || len(result.Steps) != 7 {
		t.Fatalf("retry result = %+v, err=%v, retried=%v", result, err, runs.retry)
	}
}

func TestRunServiceRestoresCompletedSource(t *testing.T) {
	plan := domainmigration.Plan{ID: uuid.New(), Status: domainmigration.PlanComplete}
	runs := &runRepositoryStub{run: domainmigration.Run{ID: uuid.New(), PlanID: plan.ID, Status: domainmigration.RunCompleted}}
	service, _ := NewRunService(&runPlanRepositoryStub{plan: plan}, runs)
	result, err := service.RestoreSource(context.Background(), runs.run.ID)
	if err != nil || !runs.restored || result.Run.Status != domainmigration.RunRollingBack {
		t.Fatalf("restore result = %+v, err=%v, restored=%v", result, err, runs.restored)
	}
}

func TestRunServiceReportIncludesOperationalSummary(t *testing.T) {
	started := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	completed := started.Add(10 * time.Second)
	plan := domainmigration.Plan{ID: uuid.New()}
	runs := &runRepositoryStub{
		run:   domainmigration.Run{ID: uuid.New(), PlanID: plan.ID, Status: domainmigration.RunCompleted, BytesTransferred: 1000, StartedAt: &started, CompletedAt: &completed},
		steps: []domainmigration.Step{{Status: domainmigration.StepSucceeded}, {Status: domainmigration.StepFailed}},
		events: []domainmigration.Event{
			{Severity: domainmigration.EventWarning}, {Severity: domainmigration.EventError},
			{Type: "CUTOVER_CONFIRMED", Severity: domainmigration.EventInfo}, {Type: "SOURCE_ROLLBACK_COMPLETED", Severity: domainmigration.EventInfo},
		},
	}
	service, _ := NewRunService(&runPlanRepositoryStub{plan: plan}, runs)
	service.clock = func() time.Time { return completed.Add(time.Hour) }
	report, err := service.Report(context.Background(), runs.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.DurationSeconds != 10 || report.Summary.AverageBytesPerSecond != 100 || report.Summary.CompletedStepCount != 1 || report.Summary.FailedStepCount != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if report.Summary.WarningCount != 1 || report.Summary.ErrorCount != 1 || !report.Summary.CutoverConfirmed || !report.Summary.SourceRollbackWasInvoked {
		t.Fatalf("unexpected report flags: %+v", report.Summary)
	}
}

var _ repository.MigrationPlanRepository = (*runPlanRepositoryStub)(nil)
var _ repository.MigrationRunRepository = (*runRepositoryStub)(nil)
