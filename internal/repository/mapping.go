package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
)

type MappingRepository interface {
	Create(context.Context, mapping.Profile) error
	Get(context.Context, uuid.UUID) (mapping.Profile, error)
	List(context.Context, *uuid.UUID) ([]mapping.Profile, error)
	Update(context.Context, mapping.Profile) error
	Delete(context.Context, uuid.UUID) error
}
