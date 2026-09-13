package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
)

type MigrationRepository interface {
	CreateRun(context.Context, migration.Run) error
	GetRun(context.Context, uuid.UUID) (migration.Run, error)
	TransitionRun(context.Context, uuid.UUID, migration.RunStatus, migration.Event) error
	EnsureStep(context.Context, migration.Step) (migration.Step, error)
	UpdateStep(context.Context, migration.Step) error
	ListSteps(context.Context, uuid.UUID) ([]migration.Step, error)
}

type MigrationRunRepository interface {
	ScheduleRun(context.Context, migration.Run, []migration.Step, bool) (migration.Run, error)
	GetRun(context.Context, uuid.UUID) (migration.Run, error)
	ListRuns(context.Context) ([]migration.RunSummary, error)
	ListSteps(context.Context, uuid.UUID) ([]migration.Step, error)
	ListEvents(context.Context, uuid.UUID, int64, int) ([]migration.Event, error)
	CancelRun(context.Context, uuid.UUID) (migration.Run, error)
	RestoreSource(context.Context, uuid.UUID) (migration.Run, error)
	ConfirmCutover(context.Context, uuid.UUID, uuid.UUID, []string) error
}

type MigrationPlanRepository interface {
	CreatePlan(context.Context, migration.Plan) error
	GetPlan(context.Context, uuid.UUID) (migration.Plan, error)
	ListPlans(context.Context) ([]migration.Plan, error)
	UpdatePlanStatus(context.Context, uuid.UUID, migration.PlanStatus) error
}

type JobRepository interface {
	Enqueue(context.Context, uuid.UUID, uuid.UUID, time.Time) (uuid.UUID, error)
	Claim(context.Context, string, time.Duration) (*migration.Lease, error)
	Heartbeat(context.Context, uuid.UUID, string, time.Duration) error
	Complete(context.Context, uuid.UUID, string) error
	Retry(context.Context, uuid.UUID, string, time.Time, string) error
	Fail(context.Context, uuid.UUID, string, string) error
}

type MigrationProgressRepository interface {
	UpsertVolumeTransfers(context.Context, uuid.UUID, []migration.VolumeTransfer) error
	ListVolumeTransfers(context.Context, uuid.UUID) ([]migration.VolumeTransfer, error)
	UpdateRunBytes(context.Context, uuid.UUID, int64, int64) error
	AppendEvent(context.Context, migration.Event) error
	SaveWorkloadReplicaSnapshots(context.Context, uuid.UUID, []migration.WorkloadReplicaSnapshot) error
	ListWorkloadReplicaSnapshots(context.Context, uuid.UUID) ([]migration.WorkloadReplicaSnapshot, error)
}

type MigrationEvidenceRepository interface {
	GetTopologyEvidence(context.Context, uuid.UUID) (migration.TopologyEvidence, bool, bool, error)
	SaveTopologyEvidence(context.Context, migration.TopologyEvidence, bool) error
	SaveCurrentTopologyObservation(context.Context, uuid.UUID, migration.CurrentTopologyObservation) error
	ListStepAttempts(context.Context, uuid.UUID) ([]migration.StepAttempt, error)
}
