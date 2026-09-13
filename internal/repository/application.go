package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/application"
)

type ApplicationRepository interface {
	Upsert(context.Context, application.SourceApplication) (application.SourceApplication, error)
	Get(context.Context, uuid.UUID) (application.SourceApplication, error)
	ListByEnvironment(context.Context, uuid.UUID) ([]application.SourceApplication, error)
}
