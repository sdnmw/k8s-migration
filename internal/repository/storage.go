package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
)

type StorageRepository interface {
	CreateStorageProfile(context.Context, storage.Profile) error
	GetStorageProfile(context.Context, uuid.UUID) (storage.Profile, error)
	ListStorageProfiles(context.Context, *uuid.UUID) ([]storage.Profile, error)
	UpdateStorageProfile(context.Context, storage.Profile) error
}
