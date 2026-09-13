package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/platform"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type PlatformRepository struct{ pool *pgxpool.Pool }

func NewPlatformRepository(pool *pgxpool.Pool) *PlatformRepository {
	return &PlatformRepository{pool: pool}
}

func (r *PlatformRepository) CreateObjectStorageProfile(ctx context.Context, value platform.ObjectStorageProfile) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO object_storage_profiles
		(id,name,endpoint,bucket,region,credential_id,tls_verify,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.ID, value.Name, value.Endpoint, value.Bucket,
		value.Region, value.CredentialID, value.TLSVerify, value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (r *PlatformRepository) GetObjectStorageProfile(ctx context.Context, id uuid.UUID) (platform.ObjectStorageProfile, error) {
	return scanObjectStorageProfile(r.pool.QueryRow(ctx, objectStorageSelect+" WHERE id=$1", id))
}

func (r *PlatformRepository) ListObjectStorageProfiles(ctx context.Context) ([]platform.ObjectStorageProfile, error) {
	rows, err := r.pool.Query(ctx, objectStorageSelect+" ORDER BY name,id")
	if err != nil {
		return nil, fmt.Errorf("list object storage profiles: %w", err)
	}
	defer rows.Close()
	result := make([]platform.ObjectStorageProfile, 0)
	for rows.Next() {
		value, scanErr := scanObjectStorageProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *PlatformRepository) UpsertAddonInstallation(ctx context.Context, value platform.AddonInstallation) error {
	if err := value.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(value.Values)
	if err != nil {
		return fmt.Errorf("encode add-on values: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO addon_installations
		(id,environment_id,type,version,status,values,message,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (environment_id,type) DO UPDATE SET
			version=EXCLUDED.version,status=EXCLUDED.status,values=EXCLUDED.values,message=EXCLUDED.message,updated_at=EXCLUDED.updated_at`,
		value.ID, value.EnvironmentID, value.Type, value.Version, value.Status, encoded, value.Message, value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (r *PlatformRepository) GetAddonInstallation(ctx context.Context, environmentID uuid.UUID, addonType platform.AddonType) (platform.AddonInstallation, error) {
	return scanAddonInstallation(r.pool.QueryRow(ctx, addonSelect+" WHERE environment_id=$1 AND type=$2", environmentID, addonType))
}

func (r *PlatformRepository) ListAddonInstallations(ctx context.Context, environmentID uuid.UUID) ([]platform.AddonInstallation, error) {
	rows, err := r.pool.Query(ctx, addonSelect+" WHERE environment_id=$1 ORDER BY type,id", environmentID)
	if err != nil {
		return nil, fmt.Errorf("list add-on installations: %w", err)
	}
	defer rows.Close()
	result := make([]platform.AddonInstallation, 0)
	for rows.Next() {
		value, scanErr := scanAddonInstallation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

const objectStorageSelect = `SELECT id,name,endpoint,bucket,region,credential_id,tls_verify,created_at,updated_at FROM object_storage_profiles`
const addonSelect = `SELECT id,environment_id,type,version,status,values,message,created_at,updated_at FROM addon_installations`

func scanObjectStorageProfile(row scanner) (platform.ObjectStorageProfile, error) {
	var value platform.ObjectStorageProfile
	if err := row.Scan(&value.ID, &value.Name, &value.Endpoint, &value.Bucket, &value.Region, &value.CredentialID, &value.TLSVerify, &value.CreatedAt, &value.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.ObjectStorageProfile{}, repository.ErrNotFound
	} else if err != nil {
		return platform.ObjectStorageProfile{}, fmt.Errorf("scan object storage profile: %w", err)
	}
	return value, nil
}

func scanAddonInstallation(row scanner) (platform.AddonInstallation, error) {
	var value platform.AddonInstallation
	var encoded []byte
	if err := row.Scan(&value.ID, &value.EnvironmentID, &value.Type, &value.Version, &value.Status, &encoded, &value.Message, &value.CreatedAt, &value.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.AddonInstallation{}, repository.ErrNotFound
	} else if err != nil {
		return platform.AddonInstallation{}, fmt.Errorf("scan add-on installation: %w", err)
	}
	if err := json.Unmarshal(encoded, &value.Values); err != nil {
		return platform.AddonInstallation{}, fmt.Errorf("decode add-on values: %w", err)
	}
	return value, nil
}
