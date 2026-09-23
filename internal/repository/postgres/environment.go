package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type EnvironmentRepository struct {
	pool *pgxpool.Pool
}

func NewEnvironmentRepository(pool *pgxpool.Pool) *EnvironmentRepository {
	return &EnvironmentRepository{pool: pool}
}

func (r *EnvironmentRepository) Create(ctx context.Context, value environment.Environment) error {
	if err := value.Validate(); err != nil {
		return err
	}
	capabilities, err := json.Marshal(value.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal environment capabilities: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO environments
		(id, name, role, kind, endpoint, credential_id, status, status_message, capabilities, capabilities_updated_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		value.ID, value.Name, value.Role, value.Kind, value.Endpoint, value.CredentialID, value.Status,
		value.StatusMessage, capabilities, value.CapabilitiesUpdatedAt, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *EnvironmentRepository) Get(ctx context.Context, id uuid.UUID) (environment.Environment, error) {
	row := r.pool.QueryRow(ctx, `SELECT id, name, role, kind, endpoint, credential_id, status, status_message,
		capabilities, capabilities_updated_at, created_at, updated_at FROM environments WHERE id = $1`, id)
	return scanEnvironment(row)
}

func (r *EnvironmentRepository) List(ctx context.Context) ([]environment.Environment, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, role, kind, endpoint, credential_id, status, status_message,
		capabilities, capabilities_updated_at, created_at, updated_at FROM environments ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	result := make([]environment.Environment, 0)
	for rows.Next() {
		value, scanErr := scanEnvironment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate environments: %w", err)
	}
	return result, nil
}

func (r *EnvironmentRepository) Update(ctx context.Context, value environment.Environment) error {
	if err := value.Validate(); err != nil {
		return err
	}
	capabilities, err := json.Marshal(value.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal environment capabilities: %w", err)
	}
	command, err := r.pool.Exec(ctx, `UPDATE environments SET name=$2, role=$3, kind=$4, endpoint=$5,
		credential_id=$6, status=$7, status_message=$8, capabilities=$9, capabilities_updated_at=$10, updated_at=$11 WHERE id=$1`,
		value.ID, value.Name, value.Role, value.Kind, value.Endpoint, value.CredentialID, value.Status,
		value.StatusMessage, capabilities, value.CapabilitiesUpdatedAt, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *EnvironmentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	var referenceCount int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM migration_plans
		WHERE source_environment_id=$1 OR target_environment_id=$1`, id).Scan(&referenceCount); err != nil {
		return fmt.Errorf("count environment migration plan references: %w", err)
	}
	if referenceCount > 0 {
		return fmt.Errorf("%w: environment is referenced by %d migration plan(s)", repository.ErrConflict, referenceCount)
	}
	command, err := r.pool.Exec(ctx, "DELETE FROM environments WHERE id=$1", id)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanEnvironment(row scanner) (environment.Environment, error) {
	var value environment.Environment
	var capabilities []byte
	err := row.Scan(&value.ID, &value.Name, &value.Role, &value.Kind, &value.Endpoint, &value.CredentialID,
		&value.Status, &value.StatusMessage, &capabilities, &value.CapabilitiesUpdatedAt, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return environment.Environment{}, repository.ErrNotFound
	}
	if err != nil {
		return environment.Environment{}, fmt.Errorf("scan environment: %w", err)
	}
	if err := json.Unmarshal(capabilities, &value.Capabilities); err != nil {
		return environment.Environment{}, fmt.Errorf("decode environment capabilities: %w", err)
	}
	return value, nil
}

func mapError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23503") {
		return fmt.Errorf("%w: %s", repository.ErrConflict, pgErr.ConstraintName)
	}
	return err
}
