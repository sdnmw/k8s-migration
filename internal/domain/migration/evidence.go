package migration

import (
	"time"

	"github.com/google/uuid"
)

type ResourceMigrationStatus string

const (
	ResourceDiscovered ResourceMigrationStatus = "DISCOVERED"
	ResourcePlanned    ResourceMigrationStatus = "PLANNED"
	ResourceCreated    ResourceMigrationStatus = "CREATED"
	ResourceSucceeded  ResourceMigrationStatus = "SUCCEEDED"
	ResourceWarning    ResourceMigrationStatus = "WARNING"
	ResourceFailed     ResourceMigrationStatus = "FAILED"
	ResourceMissing    ResourceMigrationStatus = "MISSING"
	ResourceSkipped    ResourceMigrationStatus = "SKIPPED"
	ResourceUnknown    ResourceMigrationStatus = "UNKNOWN"
)

type MappingChange struct {
	Type        string `json:"type"`
	Path        string `json:"path,omitempty"`
	SourceValue string `json:"sourceValue"`
	TargetValue string `json:"targetValue"`
	Changed     bool   `json:"changed"`
	Applied     *bool  `json:"applied,omitempty"`
}

type TopologyNode struct {
	ID             string                  `json:"id"`
	Side           string                  `json:"side"`
	APIVersion     string                  `json:"apiVersion,omitempty"`
	Kind           string                  `json:"kind"`
	Namespace      string                  `json:"namespace,omitempty"`
	Name           string                  `json:"name"`
	Required       bool                    `json:"required"`
	Status         ResourceMigrationStatus `json:"status"`
	Health         string                  `json:"health,omitempty"`
	Message        string                  `json:"message,omitempty"`
	Attributes     map[string]any          `json:"attributes,omitempty"`
	MappingChanges []MappingChange         `json:"mappingChanges,omitempty"`
}

type TopologyEdge struct {
	ID       string                  `json:"id"`
	From     string                  `json:"from"`
	To       string                  `json:"to"`
	Relation string                  `json:"relation"`
	Required bool                    `json:"required"`
	Status   ResourceMigrationStatus `json:"status,omitempty"`
	Mapping  bool                    `json:"mapping,omitempty"`
}

type TopologyGraph struct {
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Namespace string         `json:"namespace,omitempty"`
	Nodes     []TopologyNode `json:"nodes"`
	Edges     []TopologyEdge `json:"edges"`
}

type ResourceMapping struct {
	ID           string          `json:"id"`
	SourceNodeID string          `json:"sourceNodeId"`
	TargetNodeID string          `json:"targetNodeId,omitempty"`
	Relation     string          `json:"relation"`
	Changes      []MappingChange `json:"changes,omitempty"`
}

type CurrentTopologyObservation struct {
	Graph     TopologyGraph `json:"graph"`
	CheckedAt time.Time     `json:"checkedAt"`
	Error     string        `json:"error,omitempty"`
	Drifted   int           `json:"drifted"`
}

type TopologyEvidence struct {
	RunID               uuid.UUID                   `json:"migrationRunId"`
	Source              TopologyGraph               `json:"source"`
	Target              TopologyGraph               `json:"target"`
	Mappings            []ResourceMapping           `json:"mappings"`
	SnapshotOrigin      string                      `json:"snapshotOrigin"`
	SourceCapturedAt    time.Time                   `json:"sourceCapturedAt"`
	TargetCapturedAt    *time.Time                  `json:"targetCapturedAt,omitempty"`
	CurrentObservation  *CurrentTopologyObservation `json:"currentObservation,omitempty"`
	EvidenceLimitations []string                    `json:"evidenceLimitations,omitempty"`
}

type StepAttemptStatus string

const (
	AttemptRunning        StepAttemptStatus = "RUNNING"
	AttemptSucceeded      StepAttemptStatus = "SUCCEEDED"
	AttemptRetryScheduled StepAttemptStatus = "RETRY_SCHEDULED"
	AttemptFailed         StepAttemptStatus = "FAILED"
)

type StepAttempt struct {
	StepID         uuid.UUID         `json:"stepId"`
	Attempt        int               `json:"attempt"`
	Status         StepAttemptStatus `json:"status"`
	StartedAt      time.Time         `json:"startedAt"`
	CompletedAt    *time.Time        `json:"completedAt,omitempty"`
	HeartbeatAt    *time.Time        `json:"heartbeatAt,omitempty"`
	LeaseExpiresAt *time.Time        `json:"leaseExpiresAt,omitempty"`
	NextAttemptAt  *time.Time        `json:"nextAttemptAt,omitempty"`
	ErrorCode      string            `json:"errorCode,omitempty"`
	ErrorMessage   string            `json:"errorMessage,omitempty"`
	Diagnostic     string            `json:"diagnostic,omitempty"`
}

type TimelineStep struct {
	Step     Step          `json:"step"`
	Attempts []StepAttempt `json:"attempts"`
	Events   []Event       `json:"events"`
}

type RunDiagnosis struct {
	State              string     `json:"state"`
	Title              string     `json:"title"`
	Reason             string     `json:"reason,omitempty"`
	Remediation        string     `json:"remediation,omitempty"`
	FailedStepID       *uuid.UUID `json:"failedStepId,omitempty"`
	LastSuccessfulStep string     `json:"lastSuccessfulStep,omitempty"`
	LastEventAt        *time.Time `json:"lastEventAt,omitempty"`
}

type StepTimeline struct {
	RunID       uuid.UUID      `json:"migrationRunId"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Steps       []TimelineStep `json:"steps"`
	Events      []Event        `json:"events"`
	Diagnosis   RunDiagnosis   `json:"diagnosis"`
}
