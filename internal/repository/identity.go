package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
)

type IdentityRepository interface {
	AdministratorCount(context.Context) (int, error)
	CreateAdministrator(context.Context, identity.Administrator) error
	FindAdministratorByUsername(context.Context, string) (identity.Administrator, error)
	ChangeAdministratorPassword(context.Context, uuid.UUID, string, time.Time) error
	CreateSession(context.Context, identity.Session) error
	FindPrincipalByTokenHash(context.Context, []byte) (identity.Principal, error)
	DeleteSessionByTokenHash(context.Context, []byte) error
	DeleteExpiredSessions(context.Context) (int64, error)
	RecordAudit(context.Context, identity.AuditEvent) error
}
