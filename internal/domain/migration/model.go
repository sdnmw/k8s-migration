package migration

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PlanStatus string

const (
	PlanDraft    PlanStatus = "DRAFT"
	PlanReady    PlanStatus = "READY"
	PlanBlocked  PlanStatus = "BLOCKED"
	PlanRunning  PlanStatus = "RUNNING"
	PlanComplete PlanStatus = "COMPLETED"
	PlanFailed   PlanStatus = "FAILED"
)

type RunStatus string

const (
	RunPending         RunStatus = "PENDING"
	RunPreflight       RunStatus = "PREFLIGHT"
	RunPreSync         RunStatus = "PRESYNC"
	RunQuiesce         RunStatus = "QUIESCE"
	RunFinalBackup     RunStatus = "FINAL_BACKUP"
	RunTransfer        RunStatus = "TRANSFER"
	RunTransform       RunStatus = "TRANSFORM"
	RunRestore         RunStatus = "RESTORE"
	RunValidation      RunStatus = "VALIDATION"
	RunAwaitingCutover RunStatus = "AWAITING_CUTOVER"
	RunRollingBack     RunStatus = "ROLLING_BACK"
	RunCompleted       RunStatus = "COMPLETED"
	RunFailed          RunStatus = "FAILED"
	RunCancelled       RunStatus = "CANCELLED"
)

type VolumeMode string

const (
	VolumeNone         VolumeMode = "NONE"
	VolumeFSBackup     VolumeMode = "FS_BACKUP"
	VolumeCSIDataMover VolumeMode = "CSI_DATA_MOVER"
	VolumeComposeKopia VolumeMode = "COMPOSE_KOPIA"
)

type Strategy struct {
	ResourceMode               string     `json:"resourceMode"`
	VolumeMode                 VolumeMode `json:"volumeMode"`
	PreSyncEnabled             bool       `json:"preSyncEnabled"`
	OverwriteExistingResources bool       `json:"overwriteExistingResources"`
	PreserveNodePort           bool       `json:"preserveNodePort"`
	PreQuiesceHook             string     `json:"preQuiesceHook,omitempty"`
	PostRollbackHook           string     `json:"postRollbackHook,omitempty"`
}

type ValidationPolicy struct {
	RequireWorkloadsReady bool     `json:"requireWorkloadsReady"`
	RequirePVCsBound      bool     `json:"requirePVCsBound"`
	HTTPChecks            []string `json:"httpChecks,omitempty"`
	TCPChecks             []string `json:"tcpChecks,omitempty"`
	TimeoutSeconds        int      `json:"timeoutSeconds"`
}

type Plan struct {
	ID                  uuid.UUID        `json:"id"`
	Name                string           `json:"name"`
	SourceEnvironmentID uuid.UUID        `json:"sourceEnvironmentId"`
	TargetEnvironmentID uuid.UUID        `json:"targetEnvironmentId"`
	SourceApplicationID uuid.UUID        `json:"sourceApplicationId"`
	AssessmentID        uuid.UUID        `json:"assessmentId"`
	MappingProfileID    uuid.UUID        `json:"mappingProfileId"`
	Strategy            Strategy         `json:"strategy"`
	ValidationPolicy    ValidationPolicy `json:"validationPolicy"`
	Status              PlanStatus       `json:"status"`
	CreatedAt           time.Time        `json:"createdAt"`
	UpdatedAt           time.Time        `json:"updatedAt"`
}

type PreflightCheckStatus string

const (
	PreflightPassed  PreflightCheckStatus = "PASSED"
	PreflightWarning PreflightCheckStatus = "WARNING"
	PreflightBlocker PreflightCheckStatus = "BLOCKER"
)

type PreflightCheck struct {
	ID          string               `json:"id"`
	Category    string               `json:"category"`
	Status      PreflightCheckStatus `json:"status"`
	Title       string               `json:"title"`
	Message     string               `json:"message"`
	Remediation string               `json:"remediation,omitempty"`
}

type PreflightResult struct {
	PlanID       uuid.UUID        `json:"migrationPlanId"`
	Ready        bool             `json:"ready"`
	BlockerCount int              `json:"blockerCount"`
	WarningCount int              `json:"warningCount"`
	Checks       []PreflightCheck `json:"checks"`
}

type Run struct {
	ID               uuid.UUID  `json:"id"`
	PlanID           uuid.UUID  `json:"migrationPlanId"`
	RunNumber        int        `json:"runNumber"`
	Status           RunStatus  `json:"status"`
	Progress         int        `json:"progress"`
	BytesTotal       int64      `json:"bytesTotal,omitempty"`
	BytesTransferred int64      `json:"bytesTransferred,omitempty"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	ErrorCode        string     `json:"errorCode,omitempty"`
	ErrorMessage     string     `json:"errorMessage,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type RunSnapshot struct {
	Run   Run    `json:"run"`
	Steps []Step `json:"steps"`
}

// RunSummary is the denormalized, read-only projection used by the Web task
// list. Keeping this projection in the backend prevents the browser from
// issuing one request per plan, environment and application.
type RunSummary struct {
	Run
	PlanName              string `json:"planName"`
	SourceType            string `json:"sourceType"`
	SourceEnvironmentName string `json:"sourceEnvironmentName"`
	TargetEnvironmentName string `json:"targetEnvironmentName"`
	ApplicationName       string `json:"applicationName"`
	ApplicationNamespace  string `json:"applicationNamespace,omitempty"`
}

type Report struct {
	GeneratedAt     time.Time        `json:"generatedAt"`
	Summary         ReportSummary    `json:"summary"`
	Plan            Plan             `json:"plan"`
	Run             Run              `json:"run"`
	Steps           []Step           `json:"steps"`
	Events          []Event          `json:"events"`
	VolumeTransfers []VolumeTransfer `json:"volumeTransfers"`
}

type ReportSummary struct {
	Result                   RunStatus `json:"result"`
	DurationSeconds          int64     `json:"durationSeconds"`
	StepCount                int       `json:"stepCount"`
	CompletedStepCount       int       `json:"completedStepCount"`
	FailedStepCount          int       `json:"failedStepCount"`
	WarningCount             int       `json:"warningCount"`
	ErrorCount               int       `json:"errorCount"`
	BytesTransferred         int64     `json:"bytesTransferred"`
	AverageBytesPerSecond    int64     `json:"averageBytesPerSecond"`
	CutoverConfirmed         bool      `json:"cutoverConfirmed"`
	SourceRollbackWasInvoked bool      `json:"sourceRollbackWasInvoked"`
}

type ArtifactCleanupCluster struct {
	EnvironmentID   uuid.UUID `json:"environmentId"`
	Role            string    `json:"role"`
	BackupsDeleted  int       `json:"backupsDeleted"`
	RestoresDeleted int       `json:"restoresDeleted"`
}

type ArtifactCleanupResult struct {
	RunID     uuid.UUID                `json:"migrationRunId"`
	Clusters  []ArtifactCleanupCluster `json:"clusters"`
	Retained  []string                 `json:"retained,omitempty"`
	CleanedAt time.Time                `json:"cleanedAt"`
}

type StepType string

const (
	StepPreflight    StepType = "PREFLIGHT"
	StepPreSync      StepType = "PRESYNC"
	StepQuiesce      StepType = "QUIESCE"
	StepFinalBackup  StepType = "FINAL_BACKUP"
	StepTransfer     StepType = "TRANSFER"
	StepTransform    StepType = "TRANSFORM"
	StepRestore      StepType = "RESTORE"
	StepValidation   StepType = "VALIDATION"
	StepAwaitCutover StepType = "AWAIT_CUTOVER"
	StepRollback     StepType = "ROLLBACK"
)

type StepStatus string

const (
	StepPending   StepStatus = "PENDING"
	StepRunning   StepStatus = "RUNNING"
	StepSucceeded StepStatus = "SUCCEEDED"
	StepFailed    StepStatus = "FAILED"
	StepSkipped   StepStatus = "SKIPPED"
)

type Step struct {
	ID             uuid.UUID  `json:"id"`
	RunID          uuid.UUID  `json:"migrationRunId"`
	Type           StepType   `json:"type"`
	Attempt        int        `json:"attempt"`
	Status         StepStatus `json:"status"`
	Progress       int        `json:"progress"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	Summary        string     `json:"summary,omitempty"`
	IdempotencyKey string     `json:"idempotencyKey"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type EventSeverity string

const (
	EventInfo    EventSeverity = "INFO"
	EventWarning EventSeverity = "WARNING"
	EventError   EventSeverity = "ERROR"
)

type Event struct {
	ID        int64          `json:"id"`
	RunID     uuid.UUID      `json:"migrationRunId"`
	Type      string         `json:"type"`
	Severity  EventSeverity  `json:"severity"`
	Message   string         `json:"message"`
	Detail    map[string]any `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

type TransferEngine string

const (
	TransferVeleroFSB    TransferEngine = "VELERO_FSB"
	TransferCSIDataMover TransferEngine = "CSI_DATA_MOVER"
	TransferComposeKopia TransferEngine = "COMPOSE_KOPIA"
)

type TransferStatus string

const (
	TransferPending   TransferStatus = "PENDING"
	TransferRunning   TransferStatus = "RUNNING"
	TransferCompleted TransferStatus = "COMPLETED"
	TransferFailed    TransferStatus = "FAILED"
	TransferCancelled TransferStatus = "CANCELLED"
)

type VolumeTransfer struct {
	ID                       uuid.UUID      `json:"id"`
	RunID                    uuid.UUID      `json:"migrationRunId"`
	Engine                   TransferEngine `json:"engine"`
	Namespace                string         `json:"namespace"`
	SourceVolume             string         `json:"sourceVolume"`
	TargetVolume             string         `json:"targetVolume"`
	TotalBytes               int64          `json:"totalBytes"`
	TransferredBytes         int64          `json:"transferredBytes"`
	ThroughputBytesPerSecond int64          `json:"throughputBytesPerSecond"`
	RetryCount               int            `json:"retryCount"`
	ChecksumStatus           string         `json:"checksumStatus"`
	Status                   TransferStatus `json:"status"`
	ErrorMessage             string         `json:"errorMessage,omitempty"`
	CreatedAt                time.Time      `json:"createdAt"`
	UpdatedAt                time.Time      `json:"updatedAt"`
}

type WorkloadReplicaSnapshot struct {
	ID        uuid.UUID `json:"id"`
	RunID     uuid.UUID `json:"migrationRunId"`
	Namespace string    `json:"namespace"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Replicas  int32     `json:"replicas"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Lease struct {
	ID             uuid.UUID
	RunID          uuid.UUID
	StepID         uuid.UUID
	StepType       StepType
	IdempotencyKey string
	OwnerID        string
	Attempt        int
	ExpiresAt      time.Time
}

func StatusForStep(value StepType) RunStatus {
	switch value {
	case StepPreflight:
		return RunPreflight
	case StepPreSync:
		return RunPreSync
	case StepQuiesce:
		return RunQuiesce
	case StepFinalBackup:
		return RunFinalBackup
	case StepTransfer:
		return RunTransfer
	case StepTransform:
		return RunTransform
	case StepRestore:
		return RunRestore
	case StepValidation:
		return RunValidation
	case StepAwaitCutover:
		return RunAwaitingCutover
	case StepRollback:
		return RunRollingBack
	default:
		return ""
	}
}

func RequiresRollback(value RunStatus) bool {
	switch value {
	case RunQuiesce, RunFinalBackup, RunTransfer, RunTransform, RunRestore, RunValidation, RunAwaitingCutover:
		return true
	default:
		return false
	}
}

func IsTerminal(value RunStatus) bool {
	return value == RunCompleted || value == RunFailed || value == RunCancelled
}

var allowedTransitions = map[RunStatus]map[RunStatus]struct{}{
	RunPending:   {RunPreflight: {}, RunCancelled: {}},
	RunPreflight: {RunPreSync: {}, RunQuiesce: {}, RunFailed: {}, RunCancelled: {}},
	RunPreSync:   {RunQuiesce: {}, RunFailed: {}, RunCancelled: {}},
	RunQuiesce:   {RunFinalBackup: {}, RunRollingBack: {}},
	// Resource-only plans omit the data-transfer step.
	RunFinalBackup:     {RunTransfer: {}, RunTransform: {}, RunRollingBack: {}},
	RunTransfer:        {RunTransform: {}, RunRollingBack: {}},
	RunTransform:       {RunRestore: {}, RunRollingBack: {}},
	RunRestore:         {RunValidation: {}, RunRollingBack: {}},
	RunValidation:      {RunAwaitingCutover: {}, RunRollingBack: {}},
	RunAwaitingCutover: {RunCompleted: {}, RunRollingBack: {}},
	RunRollingBack:     {RunFailed: {}, RunCancelled: {}, RunCompleted: {}},
	// A completed cutover can still be deliberately reversed during the
	// operator-controlled rollback window. The target is retained for
	// diagnosis; only the source workload is restarted.
	RunCompleted: {RunRollingBack: {}},
}

func ValidateTransition(from, to RunStatus) error {
	if _, ok := allowedTransitions[from][to]; !ok {
		return fmt.Errorf("invalid migration run transition %s -> %s", from, to)
	}
	return nil
}

func (p Plan) Validate() error {
	if strings.TrimSpace(p.Name) == "" || len(strings.TrimSpace(p.Name)) > 128 {
		return errors.New("name is required and must not exceed 128 characters")
	}
	if p.SourceEnvironmentID == uuid.Nil || p.TargetEnvironmentID == uuid.Nil || p.SourceApplicationID == uuid.Nil || p.AssessmentID == uuid.Nil || p.MappingProfileID == uuid.Nil {
		return errors.New("source, target, application, assessment and mapping IDs are required")
	}
	if p.SourceEnvironmentID == p.TargetEnvironmentID {
		return errors.New("source and target environments must differ")
	}
	if p.Strategy.ResourceMode != "TRANSFORM" {
		return errors.New("resourceMode must be TRANSFORM")
	}
	switch p.Strategy.VolumeMode {
	case VolumeNone, VolumeFSBackup, VolumeCSIDataMover, VolumeComposeKopia:
	default:
		return errors.New("unsupported volumeMode")
	}
	if len(p.Strategy.PreQuiesceHook) > 4096 || len(p.Strategy.PostRollbackHook) > 4096 {
		return errors.New("migration hooks must not exceed 4096 characters")
	}
	if p.ValidationPolicy.TimeoutSeconds < 30 || p.ValidationPolicy.TimeoutSeconds > 3600 {
		return errors.New("validation timeout must be between 30 and 3600 seconds")
	}
	return nil
}
