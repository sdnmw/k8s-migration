package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/identity"
	"github.com/smartx/sks-migration-center/internal/repository"
	"github.com/smartx/sks-migration-center/internal/security"
)

type IdentityRepository struct {
	pool *pgxpool.Pool
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{pool: pool}
}

func (r *IdentityRepository) AdministratorCount(ctx context.Context) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM administrators").Scan(&count); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return count, nil
}

func (r *IdentityRepository) CreateAdministrator(ctx context.Context, value identity.Administrator) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO administrators (id,username,password_hash,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5)`, value.ID, value.Username, value.PasswordHash, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *IdentityRepository) FindAdministratorByUsername(ctx context.Context, username string) (identity.Administrator, error) {
	var value identity.Administrator
	err := r.pool.QueryRow(ctx, `SELECT id,username,password_hash,created_at,updated_at
		FROM administrators WHERE username=$1`, username).
		Scan(&value.ID, &value.Username, &value.PasswordHash, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Administrator{}, repository.ErrNotFound
	}
	if err != nil {
		return identity.Administrator{}, fmt.Errorf("find administrator: %w", err)
	}
	return value, nil
}

func (r *IdentityRepository) ChangeAdministratorPassword(ctx context.Context, administratorID uuid.UUID, passwordHash string, updatedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin administrator password change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE administrators SET password_hash=$2,updated_at=$3 WHERE id=$1`, administratorID, passwordHash, updatedAt)
	if err != nil {
		return fmt.Errorf("change administrator password: %w", err)
	}
	if result.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE administrator_id=$1`, administratorID); err != nil {
		return fmt.Errorf("invalidate administrator sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit administrator password change: %w", err)
	}
	return nil
}

func (r *IdentityRepository) CreateSession(ctx context.Context, value identity.Session) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO sessions
		(id,administrator_id,token_hash,csrf_hash,expires_at,created_at,last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, value.ID, value.AdministratorID, value.TokenHash, value.CSRFHash,
		value.ExpiresAt, value.CreatedAt, value.LastSeenAt)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *IdentityRepository) FindPrincipalByTokenHash(ctx context.Context, tokenHash []byte) (identity.Principal, error) {
	var value identity.Principal
	err := r.pool.QueryRow(ctx, `SELECT a.id,a.username,a.created_at,a.updated_at,s.id,s.csrf_hash,s.expires_at
		FROM sessions s JOIN administrators a ON a.id=s.administrator_id
		WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).
		Scan(&value.Administrator.ID, &value.Administrator.Username, &value.Administrator.CreatedAt,
			&value.Administrator.UpdatedAt, &value.SessionID, &value.CSRFHash, &value.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, repository.ErrNotFound
	}
	if err != nil {
		return identity.Principal{}, fmt.Errorf("find authenticated session: %w", err)
	}
	if _, err := r.pool.Exec(ctx, "UPDATE sessions SET last_seen_at=now() WHERE id=$1", value.SessionID); err != nil {
		return identity.Principal{}, fmt.Errorf("touch authenticated session: %w", err)
	}
	return value, nil
}

func (r *IdentityRepository) DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash=$1", tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (r *IdentityRepository) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at <= now()")
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected(), nil
}

func (r *IdentityRepository) RecordAudit(ctx context.Context, value identity.AuditEvent) error {
	detail, err := json.Marshal(security.RedactMap(value.Detail))
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO audit_events
		(actor,action,object_type,object_id,result,detail) VALUES ($1,$2,$3,$4,$5,$6)`,
		value.Actor, value.Action, value.ObjectType, value.ObjectID, value.Result, detail)
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}
