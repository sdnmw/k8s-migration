package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/credential"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type CredentialRepository struct {
	pool *pgxpool.Pool
}

func NewCredentialRepository(pool *pgxpool.Pool) *CredentialRepository {
	return &CredentialRepository{pool: pool}
}

func (r *CredentialRepository) CreateCredential(ctx context.Context, value credential.Record) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO credentials
		(id,name,type,encrypted_payload,key_version,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		value.ID, value.Name, value.Type, value.EncryptedPayload, value.KeyVersion, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *CredentialRepository) GetCredential(ctx context.Context, id uuid.UUID) (credential.Record, error) {
	var value credential.Record
	err := r.pool.QueryRow(ctx, `SELECT id,name,type,encrypted_payload,key_version,created_at,updated_at
		FROM credentials WHERE id=$1`, id).Scan(&value.ID, &value.Name, &value.Type, &value.EncryptedPayload,
		&value.KeyVersion, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return credential.Record{}, repository.ErrNotFound
	}
	if err != nil {
		return credential.Record{}, fmt.Errorf("get credential: %w", err)
	}
	return value, nil
}

func (r *CredentialRepository) ListCredentials(ctx context.Context) ([]credential.Record, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,name,type,encrypted_payload,key_version,created_at,updated_at
		FROM credentials WHERE type <> $1 ORDER BY created_at DESC`, credential.TypeCompose)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	values := make([]credential.Record, 0)
	for rows.Next() {
		var value credential.Record
		if err := rows.Scan(&value.ID, &value.Name, &value.Type, &value.EncryptedPayload, &value.KeyVersion, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *CredentialRepository) DeleteCredential(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, "DELETE FROM credentials WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	if result.RowsAffected() != 1 {
		return repository.ErrNotFound
	}
	return nil
}
