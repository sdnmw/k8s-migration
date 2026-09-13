package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	domainmigration "github.com/smartx/sks-migration-center/internal/domain/migration"
)

func (r *MigrationRepository) GetTopologyEvidence(ctx context.Context, runID uuid.UUID) (domainmigration.TopologyEvidence, bool, bool, error) {
	var encoded, observation []byte
	var terminal bool
	err := r.pool.QueryRow(ctx, `SELECT evidence,COALESCE(current_observation,'null'::jsonb),terminal_snapshot
		FROM migration_run_topologies WHERE migration_run_id=$1`, runID).Scan(&encoded, &observation, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainmigration.TopologyEvidence{}, false, false, nil
	}
	if err != nil {
		return domainmigration.TopologyEvidence{}, false, false, fmt.Errorf("get migration topology evidence: %w", err)
	}
	var value domainmigration.TopologyEvidence
	if err := json.Unmarshal(encoded, &value); err != nil {
		return domainmigration.TopologyEvidence{}, false, false, fmt.Errorf("decode migration topology evidence: %w", err)
	}
	if string(observation) != "null" {
		var current domainmigration.CurrentTopologyObservation
		if err := json.Unmarshal(observation, &current); err != nil {
			return domainmigration.TopologyEvidence{}, false, false, fmt.Errorf("decode current topology observation: %w", err)
		}
		value.CurrentObservation = &current
	}
	return value, true, terminal, nil
}

func (r *MigrationRepository) SaveCurrentTopologyObservation(ctx context.Context, runID uuid.UUID, value domainmigration.CurrentTopologyObservation) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode current topology observation: %w", err)
	}
	command, err := r.pool.Exec(ctx, `UPDATE migration_run_topologies SET current_observation=$2,updated_at=now()
		WHERE migration_run_id=$1`, runID, encoded)
	if err != nil {
		return fmt.Errorf("save current topology observation: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("topology evidence does not exist")
	}
	return nil
}

func (r *MigrationRepository) SaveTopologyEvidence(ctx context.Context, value domainmigration.TopologyEvidence, terminal bool) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode migration topology evidence: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO migration_run_topologies(migration_run_id,evidence,terminal_snapshot)
		VALUES ($1,$2,$3)
		ON CONFLICT (migration_run_id) DO UPDATE SET evidence=EXCLUDED.evidence,
		terminal_snapshot=EXCLUDED.terminal_snapshot,updated_at=now()
		WHERE migration_run_topologies.terminal_snapshot=false`, value.RunID, encoded, terminal)
	if err != nil {
		return fmt.Errorf("save migration topology evidence: %w", err)
	}
	return nil
}

func (r *MigrationRepository) ListStepAttempts(ctx context.Context, runID uuid.UUID) ([]domainmigration.StepAttempt, error) {
	rows, err := r.pool.Query(ctx, `SELECT a.step_id,a.attempt,a.status,a.started_at,a.completed_at,a.heartbeat_at,
		a.lease_expires_at,a.next_attempt_at,a.error_code,a.error_message,a.diagnostic
		FROM migration_step_attempts a JOIN migration_steps s ON s.id=a.step_id
		WHERE s.migration_run_id=$1 ORDER BY s.created_at,a.attempt`, runID)
	if err != nil {
		return nil, fmt.Errorf("list migration step attempts: %w", err)
	}
	defer rows.Close()
	values := make([]domainmigration.StepAttempt, 0)
	for rows.Next() {
		var value domainmigration.StepAttempt
		if err := rows.Scan(&value.StepID, &value.Attempt, &value.Status, &value.StartedAt, &value.CompletedAt,
			&value.HeartbeatAt, &value.LeaseExpiresAt, &value.NextAttemptAt, &value.ErrorCode,
			&value.ErrorMessage, &value.Diagnostic); err != nil {
			return nil, fmt.Errorf("scan migration step attempt: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
