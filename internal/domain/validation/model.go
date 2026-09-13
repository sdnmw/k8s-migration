package validation

import (
	"time"

	"github.com/google/uuid"
)

type Category string

const (
	CategoryWorkload    Category = "WORKLOAD"
	CategoryStorage     Category = "STORAGE"
	CategoryNetwork     Category = "NETWORK"
	CategoryApplication Category = "APPLICATION"
)

type Status string

const (
	StatusPassed  Status = "PASSED"
	StatusFailed  Status = "FAILED"
	StatusWarning Status = "WARNING"
)

type Result struct {
	ID        uuid.UUID `json:"id"`
	RunID     uuid.UUID `json:"migrationRunId"`
	Category  Category  `json:"category"`
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}
