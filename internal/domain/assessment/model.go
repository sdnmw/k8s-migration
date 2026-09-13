package assessment

import (
	"time"

	"github.com/google/uuid"
)

type Severity string

const (
	SeverityBlocker Severity = "BLOCKER"
	SeverityWarning Severity = "WARNING"
	SeverityInfo    Severity = "INFO"
)

type Category string

const (
	CategoryCompute    Category = "COMPUTE"
	CategoryStorage    Category = "STORAGE"
	CategoryNetwork    Category = "NETWORK"
	CategorySecurity   Category = "SECURITY"
	CategoryImage      Category = "IMAGE"
	CategoryAPI        Category = "API"
	CategoryDependency Category = "DEPENDENCY"
)

type Status string

const (
	StatusPending   Status = "PENDING"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
)

type Issue struct {
	ID                uuid.UUID `json:"id"`
	AssessmentID      uuid.UUID `json:"assessmentId"`
	Severity          Severity  `json:"severity"`
	Category          Category  `json:"category"`
	ResourceKind      string    `json:"resourceKind"`
	ResourceNamespace string    `json:"resourceNamespace,omitempty"`
	ResourceName      string    `json:"resourceName"`
	RuleID            string    `json:"ruleId"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Remediation       string    `json:"remediation,omitempty"`
	AutoFixable       bool      `json:"autoFixable"`
}

type Assessment struct {
	ID            uuid.UUID  `json:"id"`
	ApplicationID uuid.UUID  `json:"applicationId"`
	Score         int        `json:"score"`
	BlockerCount  int        `json:"blockerCount"`
	WarningCount  int        `json:"warningCount"`
	InfoCount     int        `json:"infoCount"`
	Status        Status     `json:"status"`
	Issues        []Issue    `json:"issues"`
	CreatedAt     time.Time  `json:"createdAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
}
