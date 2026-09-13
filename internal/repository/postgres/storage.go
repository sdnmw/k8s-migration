package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/storage"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type StorageRepository struct{ pool *pgxpool.Pool }

func NewStorageRepository(pool *pgxpool.Pool) *StorageRepository {
	return &StorageRepository{pool: pool}
}

func (r *StorageRepository) CreateStorageProfile(ctx context.Context, value storage.Profile) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO storage_profiles
		(id,environment_id,name,type,storage_class_name,provisioner,nfs_server,nfs_export,mount_options,reclaim_policy,status,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, value.ID, value.EnvironmentID, value.Name,
		value.Type, value.StorageClassName, value.Provisioner, value.NFSServer, value.NFSExport, value.MountOptions,
		value.ReclaimPolicy, value.Status, value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (r *StorageRepository) GetStorageProfile(ctx context.Context, id uuid.UUID) (storage.Profile, error) {
	return scanStorageProfile(r.pool.QueryRow(ctx, storageProfileSelect+" WHERE id=$1", id))
}

func (r *StorageRepository) ListStorageProfiles(ctx context.Context, environmentID *uuid.UUID) ([]storage.Profile, error) {
	query := storageProfileSelect
	args := []any{}
	if environmentID != nil {
		query += " WHERE environment_id=$1"
		args = append(args, *environmentID)
	}
	query += " ORDER BY name,id"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list storage profiles: %w", err)
	}
	defer rows.Close()
	result := make([]storage.Profile, 0)
	for rows.Next() {
		value, scanErr := scanStorageProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *StorageRepository) UpdateStorageProfile(ctx context.Context, value storage.Profile) error {
	if err := value.Validate(); err != nil {
		return err
	}
	command, err := r.pool.Exec(ctx, `UPDATE storage_profiles SET environment_id=$2,name=$3,type=$4,storage_class_name=$5,
		provisioner=$6,nfs_server=$7,nfs_export=$8,mount_options=$9,reclaim_policy=$10,status=$11,updated_at=$12 WHERE id=$1`,
		value.ID, value.EnvironmentID, value.Name, value.Type, value.StorageClassName, value.Provisioner, value.NFSServer,
		value.NFSExport, value.MountOptions, value.ReclaimPolicy, value.Status, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const storageProfileSelect = `SELECT id,environment_id,name,type,storage_class_name,provisioner,nfs_server,nfs_export,mount_options,reclaim_policy,status,created_at,updated_at FROM storage_profiles`

func scanStorageProfile(row scanner) (storage.Profile, error) {
	var value storage.Profile
	if err := row.Scan(&value.ID, &value.EnvironmentID, &value.Name, &value.Type, &value.StorageClassName, &value.Provisioner,
		&value.NFSServer, &value.NFSExport, &value.MountOptions, &value.ReclaimPolicy, &value.Status, &value.CreatedAt, &value.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return storage.Profile{}, repository.ErrNotFound
	} else if err != nil {
		return storage.Profile{}, fmt.Errorf("scan storage profile: %w", err)
	}
	return value, nil
}
