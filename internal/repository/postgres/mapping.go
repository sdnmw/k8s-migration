package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/mapping"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type MappingRepository struct{ pool *pgxpool.Pool }

func NewMappingRepository(pool *pgxpool.Pool) *MappingRepository {
	return &MappingRepository{pool: pool}
}

func (r *MappingRepository) Create(ctx context.Context, value mapping.Profile) error {
	values, err := mappingJSON(value)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO mapping_profiles
		(id,name,target_environment_id,storage_mappings,namespace_mappings,ingress_mappings,registry_mappings,nfs_mappings,node_label_mappings,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, value.ID, value.Name, value.TargetEnvironmentID,
		values[0], values[1], values[2], values[3], values[4], values[5], value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (r *MappingRepository) Get(ctx context.Context, id uuid.UUID) (mapping.Profile, error) {
	return scanMapping(r.pool.QueryRow(ctx, mappingSelect+" WHERE id=$1", id))
}

func (r *MappingRepository) List(ctx context.Context, targetEnvironmentID *uuid.UUID) ([]mapping.Profile, error) {
	query := mappingSelect
	args := []any{}
	if targetEnvironmentID != nil {
		query += " WHERE target_environment_id=$1"
		args = append(args, *targetEnvironmentID)
	}
	query += " ORDER BY name,id"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list mapping profiles: %w", err)
	}
	defer rows.Close()
	result := make([]mapping.Profile, 0)
	for rows.Next() {
		value, scanErr := scanMapping(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *MappingRepository) Update(ctx context.Context, value mapping.Profile) error {
	values, err := mappingJSON(value)
	if err != nil {
		return err
	}
	command, err := r.pool.Exec(ctx, `UPDATE mapping_profiles SET name=$2,target_environment_id=$3,storage_mappings=$4,
		namespace_mappings=$5,ingress_mappings=$6,registry_mappings=$7,nfs_mappings=$8,node_label_mappings=$9,updated_at=$10 WHERE id=$1`,
		value.ID, value.Name, value.TargetEnvironmentID, values[0], values[1], values[2], values[3], values[4], values[5], value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *MappingRepository) Delete(ctx context.Context, id uuid.UUID) error {
	command, err := r.pool.Exec(ctx, "DELETE FROM mapping_profiles WHERE id=$1", id)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const mappingSelect = `SELECT id,name,target_environment_id,storage_mappings,namespace_mappings,ingress_mappings,registry_mappings,nfs_mappings,node_label_mappings,created_at,updated_at FROM mapping_profiles`

func mappingJSON(value mapping.Profile) ([6][]byte, error) {
	sources := []any{value.Storage, value.Namespaces, value.Ingress, value.Registries, value.NFS, value.NodeLabels}
	var result [6][]byte
	for index, source := range sources {
		encoded, err := json.Marshal(source)
		if err != nil {
			return result, fmt.Errorf("marshal mapping profile: %w", err)
		}
		result[index] = encoded
	}
	return result, nil
}

func scanMapping(row scanner) (mapping.Profile, error) {
	var value mapping.Profile
	var encoded [6][]byte
	err := row.Scan(&value.ID, &value.Name, &value.TargetEnvironmentID, &encoded[0], &encoded[1], &encoded[2], &encoded[3], &encoded[4], &encoded[5], &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return mapping.Profile{}, repository.ErrNotFound
	}
	if err != nil {
		return mapping.Profile{}, fmt.Errorf("scan mapping profile: %w", err)
	}
	targets := []any{&value.Storage, &value.Namespaces, &value.Ingress, &value.Registries, &value.NFS, &value.NodeLabels}
	for index, target := range targets {
		if err := json.Unmarshal(encoded[index], target); err != nil {
			return mapping.Profile{}, fmt.Errorf("decode mapping profile: %w", err)
		}
	}
	return value, nil
}
