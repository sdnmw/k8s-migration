package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

type EnvironmentRepository interface {
	Create(context.Context, environment.Environment) error
	Get(context.Context, uuid.UUID) (environment.Environment, error)
	List(context.Context) ([]environment.Environment, error)
	Update(context.Context, environment.Environment) error
	Delete(context.Context, uuid.UUID) error
}
