package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type JobRepository struct {
	pool *pgxpool.Pool
}

func NewJobRepository(pool *pgxpool.Pool) *JobRepository {
	return &JobRepository{pool: pool}
}

func (r *JobRepository) Enqueue(ctx context.Context, runID, stepID uuid.UUID, availableAt time.Time) (uuid.UUID, error) {
	id := uuid.New()
	_, err := r.pool.Exec(ctx, `INSERT INTO job_leases
		(id, migration_run_id, step_id, available_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (migration_run_id, step_id) DO UPDATE SET
			available_at = LEAST(job_leases.available_at, EXCLUDED.available_at), updated_at = now()`,
		id, runID, stepID, availableAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue migration job: %w", err)
	}
	var storedID uuid.UUID
	if err := r.pool.QueryRow(ctx, "SELECT id FROM job_leases WHERE migration_run_id=$1 AND step_id=$2", runID, stepID).Scan(&storedID); err != nil {
		return uuid.Nil, fmt.Errorf("read enqueued migration job: %w", err)
	}
	return storedID, nil
}

func (r *JobRepository) Claim(ctx context.Context, ownerID string, duration time.Duration) (*migration.Lease, error) {
	if ownerID == "" || duration <= 0 {
		return nil, errors.New("ownerID and positive lease duration are required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin job claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lease migration.Lease
	err = tx.QueryRow(ctx, `SELECT j.id, j.migration_run_id, j.step_id, s.type, s.idempotency_key, j.attempt
		FROM job_leases j
		JOIN migration_steps s ON s.id = j.step_id
		WHERE j.available_at <= now()
		  AND (j.lease_expires_at IS NULL OR j.lease_expires_at < now())
		  AND s.status IN ('PENDING', 'RUNNING')
		ORDER BY j.available_at, j.created_at, j.id
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1`).Scan(&lease.ID, &lease.RunID, &lease.StepID, &lease.StepType, &lease.IdempotencyKey, &lease.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select migration job: %w", err)
	}

	err = tx.QueryRow(ctx, `UPDATE job_leases SET owner_id=$2, lease_expires_at=now()+$3::interval,
		heartbeat_at=now(), attempt=attempt+1, updated_at=now() WHERE id=$1
		RETURNING owner_id, attempt, lease_expires_at`, lease.ID, ownerID, duration.String()).
		Scan(&lease.OwnerID, &lease.Attempt, &lease.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("claim migration job: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_step_attempts
		(step_id,attempt,status,started_at,heartbeat_at,lease_expires_at)
		VALUES ($1,$2,'RUNNING',now(),now(),$3)
		ON CONFLICT (step_id,attempt) DO UPDATE SET status='RUNNING',heartbeat_at=now(),
		lease_expires_at=EXCLUDED.lease_expires_at`, lease.StepID, lease.Attempt, lease.ExpiresAt); err != nil {
		return nil, fmt.Errorf("record migration step attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='RUNNING', attempt=$2,
		started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=$1`, lease.StepID, lease.Attempt); err != nil {
		return nil, fmt.Errorf("start migration step: %w", err)
	}
	targetStatus := migration.StatusForStep(lease.StepType)
	var currentStatus migration.RunStatus
	if err := tx.QueryRow(ctx, "SELECT status FROM migration_runs WHERE id=$1 FOR UPDATE", lease.RunID).Scan(&currentStatus); err != nil {
		return nil, fmt.Errorf("lock claimed migration run: %w", err)
	}
	if targetStatus == "" {
		return nil, fmt.Errorf("unsupported migration step type %s", lease.StepType)
	}
	if currentStatus != targetStatus {
		if err := migration.ValidateTransition(currentStatus, targetStatus); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status=$2,started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=$1`, lease.RunID, targetStatus); err != nil {
			return nil, fmt.Errorf("advance claimed migration run: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'STEP_STARTED','INFO',$2,jsonb_build_object('stepId',$3::text,'stepType',$4::text,'attempt',$5::integer))`,
		lease.RunID, fmt.Sprintf("Migration step %s started", lease.StepType), lease.StepID, lease.StepType, lease.Attempt); err != nil {
		return nil, fmt.Errorf("record migration step start: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit job claim: %w", err)
	}
	return &lease, nil
}

func (r *JobRepository) Heartbeat(ctx context.Context, leaseID uuid.UUID, ownerID string, duration time.Duration) error {
	command, err := r.pool.Exec(ctx, `WITH renewed AS (
		UPDATE job_leases SET heartbeat_at=now(),lease_expires_at=now()+$3::interval,updated_at=now()
		WHERE id=$1 AND owner_id=$2 AND lease_expires_at >= now()
		RETURNING step_id,attempt,heartbeat_at,lease_expires_at)
		UPDATE migration_step_attempts a SET heartbeat_at=renewed.heartbeat_at,
		lease_expires_at=renewed.lease_expires_at FROM renewed
		WHERE a.step_id=renewed.step_id AND a.attempt=renewed.attempt`, leaseID, ownerID, duration.String())
	if err != nil {
		return fmt.Errorf("heartbeat migration job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *JobRepository) Complete(ctx context.Context, leaseID uuid.UUID, ownerID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin job completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stepID, runID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `DELETE FROM job_leases WHERE id=$1 AND owner_id=$2
		RETURNING step_id,migration_run_id,attempt`, leaseID, ownerID).Scan(&stepID, &runID, &attempt); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("complete migration job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='SUCCEEDED', progress=100,
		completed_at=now(), updated_at=now() WHERE id=$1`, stepID); err != nil {
		return fmt.Errorf("complete migration step: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_step_attempts SET status='SUCCEEDED',completed_at=now(),
		heartbeat_at=now(),lease_expires_at=NULL WHERE step_id=$1 AND attempt=$2`, stepID, attempt); err != nil {
		return fmt.Errorf("complete migration step attempt: %w", err)
	}
	var stepType migration.StepType
	if err := tx.QueryRow(ctx, "SELECT type FROM migration_steps WHERE id=$1", stepID).Scan(&stepType); err != nil {
		return fmt.Errorf("read completed migration step: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'STEP_SUCCEEDED','INFO',$2,jsonb_build_object('stepId',$3::text,'stepType',$4::text))`,
		runID, fmt.Sprintf("Migration step %s completed", stepType), stepID, stepType); err != nil {
		return fmt.Errorf("record migration step completion: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_runs SET progress=(SELECT COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','SKIPPED'))*100/COUNT(*) FROM migration_steps WHERE migration_run_id=$1),updated_at=now() WHERE id=$1`, runID); err != nil {
		return fmt.Errorf("update migration progress: %w", err)
	}
	var nextStepID uuid.UUID
	var nextStepType migration.StepType
	err = tx.QueryRow(ctx, `SELECT id,type FROM migration_steps WHERE migration_run_id=$1 AND status='PENDING'
		ORDER BY created_at,id LIMIT 1`, runID).Scan(&nextStepID, &nextStepType)
	if err == nil {
		if nextStepType == migration.StepAwaitCutover {
			if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='RUNNING',started_at=now(),summary='waiting for administrator confirmation',updated_at=now() WHERE id=$1`, nextStepID); err != nil {
				return fmt.Errorf("open cutover gate: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='AWAITING_CUTOVER',updated_at=now() WHERE id=$1`, runID); err != nil {
				return fmt.Errorf("mark migration awaiting cutover: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO migration_events
				(migration_run_id,type,severity,message,detail) VALUES ($1,'AWAITING_CUTOVER','INFO','Migration is waiting for manual traffic cutover','{}')`, runID); err != nil {
				return fmt.Errorf("record cutover gate: %w", err)
			}
		} else if _, err := tx.Exec(ctx, `INSERT INTO job_leases (id,migration_run_id,step_id,available_at) VALUES ($1,$2,$3,now())`, uuid.New(), runID, nextStepID); err != nil {
			return fmt.Errorf("enqueue next migration step: %w", err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find next migration step: %w", err)
	} else if stepType == migration.StepRollback {
		var current migration.RunStatus
		var errorCode string
		var planID uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT status,error_code,migration_plan_id FROM migration_runs WHERE id=$1 FOR UPDATE", runID).Scan(&current, &errorCode, &planID); err != nil {
			return fmt.Errorf("read finishing migration run: %w", err)
		}
		terminal, planStatus := migration.RunFailed, migration.PlanFailed
		if errorCode == "CANCEL_REQUESTED" {
			terminal, planStatus = migration.RunCancelled, migration.PlanReady
		} else if errorCode == "SOURCE_RESTORE_REQUESTED" {
			// Restoring the source after a successful cutover is an operator action,
			// not a failed/cancelled migration. The target and migration evidence stay
			// valid, so return the run and plan to their completed state.
			terminal, planStatus = migration.RunCompleted, migration.PlanComplete
		}
		if err := migration.ValidateTransition(current, terminal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status=$2,progress=100,completed_at=now(),
			error_code=CASE WHEN error_code='SOURCE_RESTORE_REQUESTED' THEN 'SOURCE_RESTORED' ELSE error_code END,
			error_message=CASE WHEN error_code='SOURCE_RESTORE_REQUESTED' THEN 'Source workload restored; target resources and migration evidence are retained' ELSE error_message END,
			updated_at=now() WHERE id=$1`, runID, terminal); err != nil {
			return fmt.Errorf("finish migration run: %w", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE migration_plans SET status=$2,updated_at=now() WHERE id=$1", planID, planStatus); err != nil {
			return fmt.Errorf("finish migration plan: %w", err)
		}
		if errorCode == "SOURCE_RESTORE_REQUESTED" {
			if _, err := tx.Exec(ctx, `INSERT INTO migration_events
				(migration_run_id,type,severity,message,detail) VALUES ($1,'SOURCE_RESTORE_COMPLETED','INFO',
				'Source workload restoration completed; target resources were retained','{}')`, runID); err != nil {
				return fmt.Errorf("record source restoration completion: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit job completion: %w", err)
	}
	return nil
}

func (r *JobRepository) Retry(ctx context.Context, leaseID uuid.UUID, ownerID string, availableAt time.Time, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration job retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID, stepID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `UPDATE job_leases SET owner_id='',lease_expires_at=NULL,
		heartbeat_at=NULL,available_at=$3,updated_at=now() WHERE id=$1 AND owner_id=$2
		RETURNING migration_run_id,step_id,attempt`, leaseID, ownerID, availableAt).Scan(&runID, &stepID, &attempt); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("retry migration job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='PENDING',summary='retry scheduled',updated_at=now() WHERE id=$1`, stepID); err != nil {
		return fmt.Errorf("mark migration step retry: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_step_attempts SET status='RETRY_SCHEDULED',completed_at=now(),
		lease_expires_at=NULL,next_attempt_at=$3,error_code='STEP_RETRY',error_message=$4,diagnostic=$4
		WHERE step_id=$1 AND attempt=$2`, stepID, attempt, availableAt, message); err != nil {
		return fmt.Errorf("record retry diagnosis: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'STEP_RETRY_SCHEDULED','WARNING','Migration step retry was scheduled',jsonb_build_object('stepId',$2::text,'attempt',$3::integer,'availableAt',$4::timestamptz,'reason',$5::text))`, runID, stepID, attempt, availableAt, message); err != nil {
		return fmt.Errorf("record migration step retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration job retry: %w", err)
	}
	return nil
}

func (r *JobRepository) Fail(ctx context.Context, leaseID uuid.UUID, ownerID string, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin job failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID, stepID uuid.UUID
	var attempt int
	if err := tx.QueryRow(ctx, `DELETE FROM job_leases WHERE id=$1 AND owner_id=$2 RETURNING migration_run_id,step_id,attempt`, leaseID, ownerID).Scan(&runID, &stepID, &attempt); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("remove failed migration job: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='FAILED',completed_at=now(),summary=$2,updated_at=now() WHERE id=$1`, stepID, message); err != nil {
		return fmt.Errorf("fail migration step: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_step_attempts SET status='FAILED',completed_at=now(),
		lease_expires_at=NULL,error_code='STEP_FAILED',error_message=$3,diagnostic=$3
		WHERE step_id=$1 AND attempt=$2`, stepID, attempt, message); err != nil {
		return fmt.Errorf("record failed step attempt: %w", err)
	}
	var status migration.RunStatus
	var planID uuid.UUID
	var stepType migration.StepType
	if err := tx.QueryRow(ctx, `SELECT r.status,r.migration_plan_id,s.type FROM migration_runs r JOIN migration_steps s ON s.id=$2 WHERE r.id=$1 FOR UPDATE OF r`, runID, stepID).Scan(&status, &planID, &stepType); err != nil {
		return fmt.Errorf("read failed migration run: %w", err)
	}
	if stepType == migration.StepRollback || !migration.RequiresRollback(status) {
		if err := migration.ValidateTransition(status, migration.RunFailed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='FAILED',completed_at=now(),error_code='STEP_FAILED',error_message=$2,updated_at=now() WHERE id=$1`, runID, message); err != nil {
			return fmt.Errorf("fail migration run: %w", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE migration_plans SET status='FAILED',updated_at=now() WHERE id=$1", planID); err != nil {
			return fmt.Errorf("fail migration plan: %w", err)
		}
	} else {
		if err := migration.ValidateTransition(status, migration.RunRollingBack); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='ROLLING_BACK',error_code='STEP_FAILED',error_message=$2,updated_at=now() WHERE id=$1`, runID, message); err != nil {
			return fmt.Errorf("require failed migration rollback: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='SKIPPED',completed_at=now(),summary='skipped after failure',updated_at=now()
			WHERE migration_run_id=$1 AND status='PENDING'`, runID); err != nil {
			return fmt.Errorf("skip steps after migration failure: %w", err)
		}
		rollbackID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO migration_steps
			(id,migration_run_id,type,attempt,status,progress,summary,idempotency_key) VALUES ($1,$2,'ROLLBACK',1,'PENDING',0,'','rollback:v1')`, rollbackID, runID); err != nil {
			return fmt.Errorf("create failure rollback step: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO job_leases (id,migration_run_id,step_id,available_at) VALUES ($1,$2,$3,now())`, uuid.New(), runID, rollbackID); err != nil {
			return fmt.Errorf("enqueue failure rollback: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'STEP_FAILED','ERROR',$2,jsonb_build_object('stepId',$3::text,'stepType',$4::text))`,
		runID, message, stepID, stepType); err != nil {
		return fmt.Errorf("record migration failure: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration failure: %w", err)
	}
	return nil
}
