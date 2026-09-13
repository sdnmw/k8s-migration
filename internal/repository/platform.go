package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
)

type PlatformRepository interface {
	CreateObjectStorageProfile(context.Context, platform.ObjectStorageProfile) error
	GetObjectStorageProfile(context.Context, uuid.UUID) (platform.ObjectStorageProfile, error)
	ListObjectStorageProfiles(context.Context) ([]platform.ObjectStorageProfile, error)
	UpsertAddonInstallation(context.Context, platform.AddonInstallation) error
	GetAddonInstallation(context.Context, uuid.UUID, platform.AddonType) (platform.AddonInstallation, error)
	ListAddonInstallations(context.Context, uuid.UUID) ([]platform.AddonInstallation, error)
}
