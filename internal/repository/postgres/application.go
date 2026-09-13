package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type ApplicationRepository struct{ pool *pgxpool.Pool }

func NewApplicationRepository(pool *pgxpool.Pool) *ApplicationRepository {
	return &ApplicationRepository{pool: pool}
}

func (r *ApplicationRepository) Upsert(ctx context.Context, value application.SourceApplication) (application.SourceApplication, error) {
	inventory, err := json.Marshal(value.Inventory)
	if err != nil {
		return application.SourceApplication{}, fmt.Errorf("marshal application inventory: %w", err)
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO source_applications
		(id,environment_id,name,source_type,namespace,inventory,definition_credential_id,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (environment_id,name,namespace) DO UPDATE SET
			source_type=EXCLUDED.source_type, inventory=EXCLUDED.inventory, definition_credential_id=EXCLUDED.definition_credential_id, updated_at=EXCLUDED.updated_at
		RETURNING id,environment_id,name,source_type,namespace,inventory,definition_credential_id,created_at,updated_at`,
		value.ID, value.EnvironmentID, value.Name, value.SourceType, value.Namespace, inventory, value.DefinitionCredentialID, value.CreatedAt, value.UpdatedAt)
	return scanApplication(row)
}

func (r *ApplicationRepository) Get(ctx context.Context, id uuid.UUID) (application.SourceApplication, error) {
	return scanApplication(r.pool.QueryRow(ctx, `SELECT id,environment_id,name,source_type,namespace,inventory,definition_credential_id,created_at,updated_at
		FROM source_applications WHERE id=$1`, id))
}

func (r *ApplicationRepository) ListByEnvironment(ctx context.Context, environmentID uuid.UUID) ([]application.SourceApplication, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,environment_id,name,source_type,namespace,inventory,definition_credential_id,created_at,updated_at
		FROM source_applications WHERE environment_id=$1 ORDER BY namespace,name,id`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list source applications: %w", err)
	}
	defer rows.Close()
	result := make([]application.SourceApplication, 0)
	for rows.Next() {
		value, scanErr := scanApplication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func scanApplication(row scanner) (application.SourceApplication, error) {
	var value application.SourceApplication
	var inventory []byte
	err := row.Scan(&value.ID, &value.EnvironmentID, &value.Name, &value.SourceType, &value.Namespace, &inventory, &value.DefinitionCredentialID, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.SourceApplication{}, repository.ErrNotFound
	}
	if err != nil {
		return application.SourceApplication{}, fmt.Errorf("scan source application: %w", err)
	}
	if err := json.Unmarshal(inventory, &value.Inventory); err != nil {
		return application.SourceApplication{}, fmt.Errorf("decode application inventory: %w", err)
	}
	return value, nil
}
