package identity

import (
	"time"

	"github.com/google/uuid"
)

type Administrator struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Session struct {
	ID              uuid.UUID
	AdministratorID uuid.UUID
	TokenHash       []byte
	CSRFHash        []byte
	ExpiresAt       time.Time
	CreatedAt       time.Time
	LastSeenAt      time.Time
}

type Principal struct {
	Administrator Administrator
	SessionID     uuid.UUID
	CSRFHash      []byte
	ExpiresAt     time.Time
}

type AuditEvent struct {
	Actor      string
	Action     string
	ObjectType string
	ObjectID   *uuid.UUID
	Result     string
	Detail     map[string]any
}
