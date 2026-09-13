package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

func (r *MigrationRepository) UpsertVolumeTransfers(ctx context.Context, runID uuid.UUID, values []migration.VolumeTransfer) error {
	if runID == uuid.Nil {
		return errors.New("migration run ID is required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin volume transfer update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for index := range values {
		value := &values[index]
		if value.ID == uuid.Nil {
			value.ID = uuid.New()
		}
		if value.ChecksumStatus == "" {
			value.ChecksumStatus = "PENDING"
		}
		_, err = tx.Exec(ctx, `INSERT INTO volume_transfers
			(id,migration_run_id,engine,namespace,source_volume,target_volume,total_bytes,transferred_bytes,
			 throughput_bytes_per_second,retry_count,checksum_status,status,error_message)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (migration_run_id,namespace,source_volume) DO UPDATE SET
			 engine=EXCLUDED.engine,target_volume=EXCLUDED.target_volume,total_bytes=EXCLUDED.total_bytes,
			 transferred_bytes=EXCLUDED.transferred_bytes,throughput_bytes_per_second=EXCLUDED.throughput_bytes_per_second,
			 retry_count=EXCLUDED.retry_count,checksum_status=EXCLUDED.checksum_status,status=EXCLUDED.status,
			 error_message=EXCLUDED.error_message,updated_at=now()`,
			value.ID, runID, value.Engine, value.Namespace, value.SourceVolume, value.TargetVolume,
			value.TotalBytes, value.TransferredBytes, value.ThroughputBytesPerSecond, value.RetryCount,
			value.ChecksumStatus, value.Status, value.ErrorMessage)
		if err != nil {
			return fmt.Errorf("upsert volume transfer: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *MigrationRepository) ListVolumeTransfers(ctx context.Context, runID uuid.UUID) ([]migration.VolumeTransfer, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,migration_run_id,engine,namespace,source_volume,target_volume,total_bytes,
		transferred_bytes,throughput_bytes_per_second,retry_count,checksum_status,status,error_message,created_at,updated_at
		FROM volume_transfers WHERE migration_run_id=$1 ORDER BY namespace,source_volume`, runID)
	if err != nil {
		return nil, fmt.Errorf("list volume transfers: %w", err)
	}
	defer rows.Close()
	result := make([]migration.VolumeTransfer, 0)
	for rows.Next() {
		var value migration.VolumeTransfer
		if err := rows.Scan(&value.ID, &value.RunID, &value.Engine, &value.Namespace, &value.SourceVolume,
			&value.TargetVolume, &value.TotalBytes, &value.TransferredBytes, &value.ThroughputBytesPerSecond,
			&value.RetryCount, &value.ChecksumStatus, &value.Status, &value.ErrorMessage, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan volume transfer: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *MigrationRepository) UpdateRunBytes(ctx context.Context, runID uuid.UUID, transferred, total int64) error {
	if transferred < 0 || total < 0 || (total > 0 && transferred > total) {
		return errors.New("migration byte progress is invalid")
	}
	command, err := r.pool.Exec(ctx, `UPDATE migration_runs SET bytes_transferred=$2,bytes_total=$3,updated_at=now() WHERE id=$1`, runID, transferred, total)
	if err != nil {
		return fmt.Errorf("update migration byte progress: %w", err)
	}
	if command.RowsAffected() != 1 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *MigrationRepository) AppendEvent(ctx context.Context, value migration.Event) error {
	if value.RunID == uuid.Nil || value.Type == "" || value.Message == "" {
		return errors.New("migration event run, type and message are required")
	}
	if value.Severity == "" {
		value.Severity = migration.EventInfo
	}
	detail, err := json.Marshal(value.Detail)
	if err != nil {
		return fmt.Errorf("marshal migration event detail: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO migration_events (migration_run_id,type,severity,message,detail) VALUES ($1,$2,$3,$4,$5)`,
		value.RunID, value.Type, value.Severity, value.Message, detail)
	if err != nil {
		return fmt.Errorf("append migration event: %w", err)
	}
	return nil
}

func (r *MigrationRepository) SaveWorkloadReplicaSnapshots(ctx context.Context, runID uuid.UUID, values []migration.WorkloadReplicaSnapshot) error {
	if runID == uuid.Nil {
		return errors.New("migration run ID is required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin workload replica snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for index := range values {
		value := &values[index]
		if value.ID == uuid.Nil {
			value.ID = uuid.New()
		}
		if value.RunID != uuid.Nil && value.RunID != runID {
			return errors.New("workload replica snapshot belongs to another migration run")
		}
		_, err := tx.Exec(ctx, `INSERT INTO workload_replica_snapshots
			(id,migration_run_id,namespace,kind,name,replicas) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (migration_run_id,namespace,kind,name) DO NOTHING`,
			value.ID, runID, value.Namespace, value.Kind, value.Name, value.Replicas)
		if err != nil {
			return fmt.Errorf("save workload replica snapshot: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *MigrationRepository) ListWorkloadReplicaSnapshots(ctx context.Context, runID uuid.UUID) ([]migration.WorkloadReplicaSnapshot, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,migration_run_id,namespace,kind,name,replicas,created_at,updated_at
		FROM workload_replica_snapshots WHERE migration_run_id=$1 ORDER BY namespace,kind,name`, runID)
	if err != nil {
		return nil, fmt.Errorf("list workload replica snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]migration.WorkloadReplicaSnapshot, 0)
	for rows.Next() {
		var value migration.WorkloadReplicaSnapshot
		if err := rows.Scan(&value.ID, &value.RunID, &value.Namespace, &value.Kind, &value.Name, &value.Replicas, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workload replica snapshot: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

var _ repository.MigrationProgressRepository = (*MigrationRepository)(nil)
