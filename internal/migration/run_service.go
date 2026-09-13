package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

var ErrInvalidRunState = errors.New("invalid migration run state")

func (s *RunService) Delete(ctx context.Context, id uuid.UUID) error {
	deleter, ok := s.runs.(interface {
		DeleteRun(context.Context, uuid.UUID) error
	})
	if !ok {
		return ErrInvalidRunState
	}
	return deleter.DeleteRun(ctx, id)
}

type RunService struct {
	plans    repository.MigrationPlanRepository
	runs     repository.MigrationRunRepository
	progress repository.MigrationProgressRepository
	clock    func() time.Time
	newID    func() uuid.UUID
	evidence RunEvidenceCapture
}

type RunEvidenceCapture interface {
	CaptureInitial(context.Context, uuid.UUID) error
}

func NewRunService(plans repository.MigrationPlanRepository, runs repository.MigrationRunRepository, progress ...repository.MigrationProgressRepository) (*RunService, error) {
	if plans == nil || runs == nil {
		return nil, errors.New("migration plan and run repositories are required")
	}
	var progressRepository repository.MigrationProgressRepository
	if len(progress) > 0 {
		progressRepository = progress[0]
	}
	return &RunService{plans: plans, runs: runs, progress: progressRepository, clock: func() time.Time { return time.Now().UTC() }, newID: uuid.New}, nil
}

func (s *RunService) ConfigureEvidence(value RunEvidenceCapture) {
	s.evidence = value
}

func (s *RunService) VolumeTransfers(ctx context.Context, runID uuid.UUID) ([]domainmigration.VolumeTransfer, error) {
	if runID == uuid.Nil {
		return nil, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	if s.progress == nil {
		return []domainmigration.VolumeTransfer{}, nil
	}
	if _, err := s.runs.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	return s.progress.ListVolumeTransfers(ctx, runID)
}

func (s *RunService) Start(ctx context.Context, planID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if planID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: planId is required", ErrInvalidInput)
	}
	plan, err := s.plans.GetPlan(ctx, planID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	if plan.Status != domainmigration.PlanReady {
		return domainmigration.RunSnapshot{}, ErrInvalidRunState
	}
	return s.schedule(ctx, plan, false)
}

func (s *RunService) Retry(ctx context.Context, runID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if runID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	previous, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	if previous.Status != domainmigration.RunFailed && previous.Status != domainmigration.RunCancelled && previous.Status != domainmigration.RunCompleted {
		return domainmigration.RunSnapshot{}, ErrInvalidRunState
	}
	plan, err := s.plans.GetPlan(ctx, previous.PlanID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	return s.schedule(ctx, plan, true)
}

// RestoreSource schedules the same idempotent source-side recovery used by an
// automatic rollback, but makes it available after manual cutover confirmation.
// Target resources and migration evidence are deliberately retained.
func (s *RunService) RestoreSource(ctx context.Context, runID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if runID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	if _, err := s.runs.RestoreSource(ctx, runID); err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	return s.Get(ctx, runID)
}

func (s *RunService) Get(ctx context.Context, runID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if runID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	steps, err := s.runs.ListSteps(ctx, runID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	return domainmigration.RunSnapshot{Run: run, Steps: steps}, nil
}

func (s *RunService) List(ctx context.Context) ([]domainmigration.RunSummary, error) {
	return s.runs.ListRuns(ctx)
}

func (s *RunService) Cancel(ctx context.Context, runID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if runID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	if _, err := s.runs.CancelRun(ctx, runID); err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	return s.Get(ctx, runID)
}

func (s *RunService) Rollback(ctx context.Context, runID uuid.UUID) (domainmigration.RunSnapshot, error) {
	if runID == uuid.Nil {
		return domainmigration.RunSnapshot{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	if !domainmigration.RequiresRollback(run.Status) {
		return domainmigration.RunSnapshot{}, ErrInvalidRunState
	}
	if _, err := s.runs.CancelRun(ctx, runID); err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	return s.Get(ctx, runID)
}

func (s *RunService) ConfirmCutover(ctx context.Context, runID, administratorID uuid.UUID) error {
	if runID == uuid.Nil || administratorID == uuid.Nil {
		return fmt.Errorf("%w: runId and administrator are required", ErrInvalidInput)
	}
	return s.runs.ConfirmCutover(ctx, runID, administratorID, []string{
		"target validation passed", "external DNS/load balancer cutover completed", "source rollback window acknowledged",
	})
}

func (s *RunService) Report(ctx context.Context, runID uuid.UUID) (domainmigration.Report, error) {
	if runID == uuid.Nil {
		return domainmigration.Report{}, fmt.Errorf("%w: runId is required", ErrInvalidInput)
	}
	run, err := s.runs.GetRun(ctx, runID)
	if err != nil {
		return domainmigration.Report{}, err
	}
	plan, err := s.plans.GetPlan(ctx, run.PlanID)
	if err != nil {
		return domainmigration.Report{}, err
	}
	steps, err := s.runs.ListSteps(ctx, runID)
	if err != nil {
		return domainmigration.Report{}, err
	}
	events := make([]domainmigration.Event, 0)
	var afterID int64
	for {
		batch, listErr := s.runs.ListEvents(ctx, runID, afterID, 500)
		if listErr != nil {
			return domainmigration.Report{}, listErr
		}
		events = append(events, batch...)
		if len(batch) < 500 {
			break
		}
		afterID = batch[len(batch)-1].ID
	}
	transfers := []domainmigration.VolumeTransfer{}
	if s.progress != nil {
		transfers, err = s.progress.ListVolumeTransfers(ctx, runID)
		if err != nil {
			return domainmigration.Report{}, err
		}
	}
	generatedAt := s.clock()
	return domainmigration.Report{
		GeneratedAt: generatedAt, Summary: reportSummary(run, steps, events, generatedAt),
		Plan: plan, Run: run, Steps: steps, Events: events, VolumeTransfers: transfers,
	}, nil
}

func reportSummary(run domainmigration.Run, steps []domainmigration.Step, events []domainmigration.Event, generatedAt time.Time) domainmigration.ReportSummary {
	result := domainmigration.ReportSummary{Result: run.Status, StepCount: len(steps), BytesTransferred: run.BytesTransferred}
	for _, step := range steps {
		switch step.Status {
		case domainmigration.StepSucceeded, domainmigration.StepSkipped:
			result.CompletedStepCount++
		case domainmigration.StepFailed:
			result.FailedStepCount++
		}
	}
	for _, event := range events {
		switch event.Severity {
		case domainmigration.EventWarning:
			result.WarningCount++
		case domainmigration.EventError:
			result.ErrorCount++
		}
		if event.Type == "CUTOVER_CONFIRMED" {
			result.CutoverConfirmed = true
		}
		if event.Type == "SOURCE_ROLLBACK_COMPLETED" || event.Type == "COMPOSE_SOURCE_RESTARTED" {
			result.SourceRollbackWasInvoked = true
		}
	}
	if run.StartedAt != nil {
		endedAt := generatedAt
		if run.CompletedAt != nil {
			endedAt = *run.CompletedAt
		}
		if endedAt.After(*run.StartedAt) {
			result.DurationSeconds = int64(endedAt.Sub(*run.StartedAt).Seconds())
			if result.DurationSeconds > 0 {
				result.AverageBytesPerSecond = result.BytesTransferred / result.DurationSeconds
			}
		}
	}
	return result
}

func (s *RunService) Events(ctx context.Context, runID uuid.UUID, afterID int64, limit int) ([]domainmigration.Event, error) {
	if runID == uuid.Nil || afterID < 0 {
		return nil, fmt.Errorf("%w: runId and non-negative event cursor are required", ErrInvalidInput)
	}
	return s.runs.ListEvents(ctx, runID, afterID, limit)
}

func (s *RunService) schedule(ctx context.Context, plan domainmigration.Plan, retry bool) (domainmigration.RunSnapshot, error) {
	now := s.clock()
	run := domainmigration.Run{ID: s.newID(), PlanID: plan.ID, Status: domainmigration.RunPending, CreatedAt: now, UpdatedAt: now}
	steps := scheduledSteps(plan, run.ID, now, s.newID)
	stored, err := s.runs.ScheduleRun(ctx, run, steps, retry)
	if err != nil {
		return domainmigration.RunSnapshot{}, err
	}
	for index := range steps {
		steps[index].RunID = stored.ID
	}
	if s.evidence != nil {
		if err := s.evidence.CaptureInitial(ctx, stored.ID); err != nil {
			return domainmigration.RunSnapshot{}, fmt.Errorf("capture initial migration evidence: %w", err)
		}
	}
	return domainmigration.RunSnapshot{Run: stored, Steps: steps}, nil
}

func scheduledSteps(plan domainmigration.Plan, runID uuid.UUID, now time.Time, newID func() uuid.UUID) []domainmigration.Step {
	types := []domainmigration.StepType{domainmigration.StepPreflight}
	if plan.Strategy.PreSyncEnabled {
		types = append(types, domainmigration.StepPreSync)
	}
	types = append(types, domainmigration.StepQuiesce, domainmigration.StepFinalBackup)
	if plan.Strategy.VolumeMode != domainmigration.VolumeNone {
		types = append(types, domainmigration.StepTransfer)
	}
	types = append(types, domainmigration.StepTransform, domainmigration.StepRestore, domainmigration.StepValidation, domainmigration.StepAwaitCutover)
	steps := make([]domainmigration.Step, 0, len(types))
	for index, stepType := range types {
		steps = append(steps, domainmigration.Step{
			ID: newID(), RunID: runID, Type: stepType, Attempt: 1, Status: domainmigration.StepPending,
			IdempotencyKey: fmt.Sprintf("%s:v1", stepType), CreatedAt: now.Add(time.Duration(index) * time.Millisecond), UpdatedAt: now,
		})
	}
	return steps
}
