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
	"github.com/smartx/sks-migration-center/internal/domain/migration"
	"github.com/smartx/sks-migration-center/internal/repository"
)

type MigrationRepository struct {
	pool *pgxpool.Pool
}

func (r *MigrationRepository) DeleteRun(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `UPDATE migration_runs SET deleted_at=now() WHERE id=$1 AND status IN ('COMPLETED','FAILED','CANCELLED')`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return repository.ErrConflict
	}
	return nil
}

func NewMigrationRepository(pool *pgxpool.Pool) *MigrationRepository {
	return &MigrationRepository{pool: pool}
}

func (r *MigrationRepository) CreatePlan(ctx context.Context, value migration.Plan) error {
	strategy, err := json.Marshal(value.Strategy)
	if err != nil {
		return fmt.Errorf("marshal migration strategy: %w", err)
	}
	validationPolicy, err := json.Marshal(value.ValidationPolicy)
	if err != nil {
		return fmt.Errorf("marshal validation policy: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO migration_plans
		(id,name,source_environment_id,target_environment_id,source_application_id,assessment_id,
		 mapping_profile_id,strategy,validation_policy,status,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, value.ID, value.Name,
		value.SourceEnvironmentID, value.TargetEnvironmentID, value.SourceApplicationID, value.AssessmentID,
		value.MappingProfileID, strategy, validationPolicy, value.Status, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *MigrationRepository) GetPlan(ctx context.Context, id uuid.UUID) (migration.Plan, error) {
	return scanMigrationPlan(r.pool.QueryRow(ctx, `SELECT id,name,source_environment_id,target_environment_id,
		source_application_id,assessment_id,mapping_profile_id,strategy,validation_policy,status,created_at,updated_at
		FROM migration_plans WHERE id=$1`, id))
}

func (r *MigrationRepository) ListPlans(ctx context.Context) ([]migration.Plan, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,name,source_environment_id,target_environment_id,
		source_application_id,assessment_id,mapping_profile_id,strategy,validation_policy,status,created_at,updated_at
		FROM migration_plans ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list migration plans: %w", err)
	}
	defer rows.Close()
	result := make([]migration.Plan, 0)
	for rows.Next() {
		value, scanErr := scanMigrationPlan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *MigrationRepository) UpdatePlanStatus(ctx context.Context, id uuid.UUID, status migration.PlanStatus) error {
	command, err := r.pool.Exec(ctx, `UPDATE migration_plans SET status=$2,updated_at=now()
		WHERE id=$1 AND status IN ('DRAFT','READY','BLOCKED')`, id, status)
	if err != nil {
		return fmt.Errorf("update migration plan status: %w", err)
	}
	if command.RowsAffected() != 1 {
		var exists bool
		if err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM migration_plans WHERE id=$1)", id).Scan(&exists); err != nil {
			return fmt.Errorf("check migration plan: %w", err)
		}
		if !exists {
			return repository.ErrNotFound
		}
		return repository.ErrConflict
	}
	return nil
}

func scanMigrationPlan(row scanner) (migration.Plan, error) {
	var value migration.Plan
	var strategy, validationPolicy []byte
	err := row.Scan(&value.ID, &value.Name, &value.SourceEnvironmentID, &value.TargetEnvironmentID,
		&value.SourceApplicationID, &value.AssessmentID, &value.MappingProfileID, &strategy, &validationPolicy,
		&value.Status, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return migration.Plan{}, repository.ErrNotFound
	}
	if err != nil {
		return migration.Plan{}, fmt.Errorf("scan migration plan: %w", err)
	}
	if err := json.Unmarshal(strategy, &value.Strategy); err != nil {
		return migration.Plan{}, fmt.Errorf("decode migration strategy: %w", err)
	}
	if err := json.Unmarshal(validationPolicy, &value.ValidationPolicy); err != nil {
		return migration.Plan{}, fmt.Errorf("decode validation policy: %w", err)
	}
	return value, nil
}

func (r *MigrationRepository) CreateRun(ctx context.Context, value migration.Run) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO migration_runs
		(id, migration_plan_id, run_number, status, progress, bytes_total, bytes_transferred,
		 started_at, completed_at, error_code, error_message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, value.ID, value.PlanID, value.RunNumber,
		value.Status, value.Progress, value.BytesTotal, value.BytesTransferred, value.StartedAt,
		value.CompletedAt, value.ErrorCode, value.ErrorMessage)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (r *MigrationRepository) ScheduleRun(ctx context.Context, value migration.Run, steps []migration.Step, retry bool) (migration.Run, error) {
	if value.ID == uuid.Nil || value.PlanID == uuid.Nil || len(steps) == 0 {
		return migration.Run{}, errors.New("run, plan and at least one step are required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return migration.Run{}, fmt.Errorf("begin migration scheduling: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var planStatus migration.PlanStatus
	if err := tx.QueryRow(ctx, "SELECT status FROM migration_plans WHERE id=$1 FOR UPDATE", value.PlanID).Scan(&planStatus); errors.Is(err, pgx.ErrNoRows) {
		return migration.Run{}, repository.ErrNotFound
	} else if err != nil {
		return migration.Run{}, fmt.Errorf("lock migration plan: %w", err)
	}
	allowed := planStatus == migration.PlanReady
	if retry {
		allowed = planStatus == migration.PlanReady || planStatus == migration.PlanRunning || planStatus == migration.PlanFailed || planStatus == migration.PlanComplete
	}
	if !allowed {
		return migration.Run{}, repository.ErrConflict
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(run_number),0)+1 FROM migration_runs WHERE migration_plan_id=$1`, value.PlanID).Scan(&value.RunNumber); err != nil {
		return migration.Run{}, fmt.Errorf("allocate migration run number: %w", err)
	}
	now := value.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	value.CreatedAt, value.UpdatedAt = now, now
	value.Status = migration.RunPending
	value.Progress = 0
	if _, err := tx.Exec(ctx, `INSERT INTO migration_runs
		(id,migration_plan_id,run_number,status,progress,bytes_total,bytes_transferred,error_code,error_message,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, value.ID, value.PlanID, value.RunNumber,
		value.Status, value.Progress, value.BytesTotal, value.BytesTransferred, value.ErrorCode, value.ErrorMessage, now); err != nil {
		return migration.Run{}, mapError(err)
	}
	for index := range steps {
		step := &steps[index]
		step.RunID = value.ID
		if step.ID == uuid.Nil {
			step.ID = uuid.New()
		}
		step.Attempt, step.Status, step.Progress = 1, migration.StepPending, 0
		step.CreatedAt, step.UpdatedAt = now.Add(time.Duration(index)*time.Millisecond), now
		if _, err := tx.Exec(ctx, `INSERT INTO migration_steps
			(id,migration_run_id,type,attempt,status,progress,summary,idempotency_key,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, step.ID, step.RunID, step.Type, step.Attempt,
			step.Status, step.Progress, step.Summary, step.IdempotencyKey, step.CreatedAt, step.UpdatedAt); err != nil {
			return migration.Run{}, fmt.Errorf("create scheduled migration step: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO job_leases (id,migration_run_id,step_id,available_at)
		VALUES ($1,$2,$3,now())`, uuid.New(), value.ID, steps[0].ID); err != nil {
		return migration.Run{}, fmt.Errorf("enqueue first migration step: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE migration_plans SET status='RUNNING',updated_at=$2 WHERE id=$1", value.PlanID, now); err != nil {
		return migration.Run{}, fmt.Errorf("mark migration plan running: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'RUN_CREATED','INFO',$2,$3)`, value.ID,
		fmt.Sprintf("Migration run #%d was scheduled", value.RunNumber), []byte(`{"status":"PENDING"}`)); err != nil {
		return migration.Run{}, fmt.Errorf("record migration scheduling: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return migration.Run{}, fmt.Errorf("commit migration scheduling: %w", err)
	}
	return value, nil
}

func (r *MigrationRepository) GetRun(ctx context.Context, id uuid.UUID) (migration.Run, error) {
	var value migration.Run
	err := r.pool.QueryRow(ctx, `SELECT id, migration_plan_id, run_number, status, progress,
		bytes_total, bytes_transferred, started_at, completed_at, error_code, error_message,created_at,updated_at
		FROM migration_runs WHERE id=$1`, id).Scan(&value.ID, &value.PlanID, &value.RunNumber,
		&value.Status, &value.Progress, &value.BytesTotal, &value.BytesTransferred, &value.StartedAt,
		&value.CompletedAt, &value.ErrorCode, &value.ErrorMessage, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return migration.Run{}, repository.ErrNotFound
	}
	if err != nil {
		return migration.Run{}, fmt.Errorf("get migration run: %w", err)
	}
	return value, nil
}

func (r *MigrationRepository) ListRuns(ctx context.Context) ([]migration.RunSummary, error) {
	rows, err := r.pool.Query(ctx, `SELECT mr.id,mr.migration_plan_id,mr.run_number,mr.status,mr.progress,
		mr.bytes_total,mr.bytes_transferred,mr.started_at,mr.completed_at,mr.error_code,mr.error_message,
		mr.created_at,mr.updated_at,mp.name,sa.source_type,se.name,te.name,sa.name,sa.namespace
		FROM migration_runs mr
		JOIN migration_plans mp ON mp.id=mr.migration_plan_id
		JOIN environments se ON se.id=mp.source_environment_id
		JOIN environments te ON te.id=mp.target_environment_id
		JOIN source_applications sa ON sa.id=mp.source_application_id
		WHERE mr.deleted_at IS NULL
		ORDER BY mr.created_at DESC,mr.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list migration runs: %w", err)
	}
	defer rows.Close()
	result := make([]migration.RunSummary, 0)
	for rows.Next() {
		var value migration.RunSummary
		if err := rows.Scan(&value.ID, &value.PlanID, &value.RunNumber, &value.Status, &value.Progress,
			&value.BytesTotal, &value.BytesTransferred, &value.StartedAt, &value.CompletedAt,
			&value.ErrorCode, &value.ErrorMessage, &value.CreatedAt, &value.UpdatedAt, &value.PlanName,
			&value.SourceType, &value.SourceEnvironmentName, &value.TargetEnvironmentName,
			&value.ApplicationName, &value.ApplicationNamespace); err != nil {
			return nil, fmt.Errorf("scan migration run summary: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list migration runs: %w", err)
	}
	return result, nil
}

func (r *MigrationRepository) TransitionRun(ctx context.Context, id uuid.UUID, to migration.RunStatus, event migration.Event) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from migration.RunStatus
	if err := tx.QueryRow(ctx, "SELECT status FROM migration_runs WHERE id=$1 FOR UPDATE", id).Scan(&from); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock migration run: %w", err)
	}
	if err := migration.ValidateTransition(from, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status=$2,
		started_at=CASE WHEN $2='PREFLIGHT' THEN COALESCE(started_at, now()) ELSE started_at END,
		completed_at=CASE WHEN $2 IN ('COMPLETED','FAILED','CANCELLED') THEN now() ELSE NULL END,
		updated_at=now() WHERE id=$1`, id, to); err != nil {
		return fmt.Errorf("update migration run: %w", err)
	}
	detail, err := json.Marshal(event.Detail)
	if err != nil {
		return fmt.Errorf("marshal migration event: %w", err)
	}
	if event.Type == "" {
		event.Type = "RUN_STATUS_CHANGED"
	}
	if event.Severity == "" {
		event.Severity = migration.EventInfo
	}
	if event.Message == "" {
		event.Message = fmt.Sprintf("Migration status changed from %s to %s", from, to)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id, type, severity, message, detail) VALUES ($1,$2,$3,$4,$5)`,
		id, event.Type, event.Severity, event.Message, detail); err != nil {
		return fmt.Errorf("insert migration event: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *MigrationRepository) EnsureStep(ctx context.Context, value migration.Step) (migration.Step, error) {
	_, err := r.pool.Exec(ctx, `INSERT INTO migration_steps
		(id, migration_run_id, type, attempt, status, progress, started_at, completed_at, summary,
		 idempotency_key, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (migration_run_id, idempotency_key) DO NOTHING`, value.ID, value.RunID, value.Type,
		value.Attempt, value.Status, value.Progress, value.StartedAt, value.CompletedAt, value.Summary,
		value.IdempotencyKey, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return migration.Step{}, fmt.Errorf("ensure migration step: %w", err)
	}
	var stored migration.Step
	err = r.pool.QueryRow(ctx, `SELECT id, migration_run_id, type, attempt, status, progress,
		started_at, completed_at, summary, idempotency_key, created_at, updated_at
		FROM migration_steps WHERE migration_run_id=$1 AND idempotency_key=$2`, value.RunID, value.IdempotencyKey).
		Scan(&stored.ID, &stored.RunID, &stored.Type, &stored.Attempt, &stored.Status, &stored.Progress,
			&stored.StartedAt, &stored.CompletedAt, &stored.Summary, &stored.IdempotencyKey, &stored.CreatedAt, &stored.UpdatedAt)
	if err != nil {
		return migration.Step{}, fmt.Errorf("read migration step: %w", err)
	}
	return stored, nil
}

func (r *MigrationRepository) UpdateStep(ctx context.Context, value migration.Step) error {
	command, err := r.pool.Exec(ctx, `UPDATE migration_steps SET attempt=$2, status=$3, progress=$4,
		started_at=$5, completed_at=$6, summary=$7, updated_at=$8 WHERE id=$1`, value.ID, value.Attempt,
		value.Status, value.Progress, value.StartedAt, value.CompletedAt, value.Summary, value.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update migration step: %w", err)
	}
	if command.RowsAffected() != 1 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *MigrationRepository) ListSteps(ctx context.Context, runID uuid.UUID) ([]migration.Step, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, migration_run_id, type, attempt, status, progress,
		started_at, completed_at, summary, idempotency_key, created_at, updated_at
		FROM migration_steps WHERE migration_run_id=$1 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list migration steps: %w", err)
	}
	defer rows.Close()
	result := make([]migration.Step, 0)
	for rows.Next() {
		var value migration.Step
		if err := rows.Scan(&value.ID, &value.RunID, &value.Type, &value.Attempt, &value.Status,
			&value.Progress, &value.StartedAt, &value.CompletedAt, &value.Summary, &value.IdempotencyKey,
			&value.CreatedAt, &value.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan migration step: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *MigrationRepository) ListEvents(ctx context.Context, runID uuid.UUID, afterID int64, limit int) ([]migration.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM migration_runs WHERE id=$1)", runID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check migration run events: %w", err)
	}
	if !exists {
		return nil, repository.ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT id,migration_run_id,type,severity,message,detail,created_at
		FROM migration_events WHERE migration_run_id=$1 AND id>$2 ORDER BY id LIMIT $3`, runID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list migration events: %w", err)
	}
	defer rows.Close()
	result := make([]migration.Event, 0)
	for rows.Next() {
		var value migration.Event
		var detail []byte
		if err := rows.Scan(&value.ID, &value.RunID, &value.Type, &value.Severity, &value.Message, &detail, &value.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan migration event: %w", err)
		}
		if err := json.Unmarshal(detail, &value.Detail); err != nil {
			return nil, fmt.Errorf("decode migration event: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *MigrationRepository) CancelRun(ctx context.Context, id uuid.UUID) (migration.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return migration.Run{}, fmt.Errorf("begin migration cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status migration.RunStatus
	var planID uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT status,migration_plan_id FROM migration_runs WHERE id=$1 FOR UPDATE", id).Scan(&status, &planID); errors.Is(err, pgx.ErrNoRows) {
		return migration.Run{}, repository.ErrNotFound
	} else if err != nil {
		return migration.Run{}, fmt.Errorf("lock migration run for cancellation: %w", err)
	}
	if migration.IsTerminal(status) || status == migration.RunRollingBack {
		return migration.Run{}, repository.ErrConflict
	}
	now := time.Now().UTC()
	if migration.RequiresRollback(status) {
		if err := migration.ValidateTransition(status, migration.RunRollingBack); err != nil {
			return migration.Run{}, repository.ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='ROLLING_BACK',error_code='CANCEL_REQUESTED',
			error_message='Cancellation requested after source quiesce',updated_at=$2 WHERE id=$1`, id, now); err != nil {
			return migration.Run{}, fmt.Errorf("mark migration rollback: %w", err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM job_leases WHERE migration_run_id=$1", id); err != nil {
			return migration.Run{}, fmt.Errorf("cancel active migration leases: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='SKIPPED',completed_at=$2,summary='cancelled by administrator',updated_at=$2
			WHERE migration_run_id=$1 AND status IN ('PENDING','RUNNING')`, id, now); err != nil {
			return migration.Run{}, fmt.Errorf("skip cancelled migration steps: %w", err)
		}
		stepID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO migration_steps
			(id,migration_run_id,type,attempt,status,progress,summary,idempotency_key,created_at,updated_at)
			VALUES ($1,$2,'ROLLBACK',1,'PENDING',0,'','rollback:v1',$3,$3)
			ON CONFLICT (migration_run_id,idempotency_key) DO UPDATE SET status='PENDING',updated_at=$3`, stepID, id, now); err != nil {
			return migration.Run{}, fmt.Errorf("create rollback step: %w", err)
		}
		if err := tx.QueryRow(ctx, "SELECT id FROM migration_steps WHERE migration_run_id=$1 AND idempotency_key='rollback:v1'", id).Scan(&stepID); err != nil {
			return migration.Run{}, fmt.Errorf("read rollback step: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO job_leases (id,migration_run_id,step_id,available_at) VALUES ($1,$2,$3,clock_timestamp())
			ON CONFLICT (migration_run_id,step_id) DO UPDATE SET owner_id='',lease_expires_at=NULL,heartbeat_at=NULL,
			available_at=clock_timestamp(),updated_at=clock_timestamp()`, uuid.New(), id, stepID); err != nil {
			return migration.Run{}, fmt.Errorf("enqueue rollback step: %w", err)
		}
	} else {
		if err := migration.ValidateTransition(status, migration.RunCancelled); err != nil {
			return migration.Run{}, repository.ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='CANCELLED',completed_at=$2,error_code='CANCEL_REQUESTED',
			error_message='Cancelled by administrator',updated_at=$2 WHERE id=$1`, id, now); err != nil {
			return migration.Run{}, fmt.Errorf("cancel migration run: %w", err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM job_leases WHERE migration_run_id=$1", id); err != nil {
			return migration.Run{}, fmt.Errorf("remove cancelled migration leases: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='SKIPPED',completed_at=$2,summary='cancelled by administrator',updated_at=$2
			WHERE migration_run_id=$1 AND status IN ('PENDING','RUNNING')`, id, now); err != nil {
			return migration.Run{}, fmt.Errorf("skip cancelled migration steps: %w", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE migration_plans SET status='READY',updated_at=$2 WHERE id=$1", planID, now); err != nil {
			return migration.Run{}, fmt.Errorf("release cancelled migration plan: %w", err)
		}
	}
	message := "Migration run was cancelled before source quiesce"
	if migration.RequiresRollback(status) {
		message = "Migration cancellation requires source rollback"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'CANCEL_REQUESTED','WARNING',$2,$3)`, id, message, []byte(`{}`)); err != nil {
		return migration.Run{}, fmt.Errorf("record migration cancellation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return migration.Run{}, fmt.Errorf("commit migration cancellation: %w", err)
	}
	return r.GetRun(ctx, id)
}

func (r *MigrationRepository) RestoreSource(ctx context.Context, id uuid.UUID) (migration.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return migration.Run{}, fmt.Errorf("begin source restoration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status migration.RunStatus
	if err := tx.QueryRow(ctx, "SELECT status FROM migration_runs WHERE id=$1 FOR UPDATE", id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return migration.Run{}, repository.ErrNotFound
	} else if err != nil {
		return migration.Run{}, fmt.Errorf("lock migration run for source restoration: %w", err)
	}
	if status != migration.RunCompleted {
		return migration.Run{}, repository.ErrConflict
	}
	if err := migration.ValidateTransition(status, migration.RunRollingBack); err != nil {
		return migration.Run{}, repository.ErrConflict
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='ROLLING_BACK',completed_at=NULL,
		error_code='SOURCE_RESTORE_REQUESTED',error_message='Source restoration requested after cutover',updated_at=$2 WHERE id=$1`, id, now); err != nil {
		return migration.Run{}, fmt.Errorf("mark source restoration: %w", err)
	}
	stepID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO migration_steps
		(id,migration_run_id,type,attempt,status,progress,summary,idempotency_key,created_at,updated_at)
		VALUES ($1,$2,'ROLLBACK',1,'PENDING',0,'','restore-source:v1',$3,$3)
		ON CONFLICT (migration_run_id,idempotency_key) DO UPDATE SET attempt=migration_steps.attempt+1,
		status='PENDING',progress=0,started_at=NULL,completed_at=NULL,summary='',updated_at=$3`, stepID, id, now); err != nil {
		return migration.Run{}, fmt.Errorf("create source restoration step: %w", err)
	}
	if err := tx.QueryRow(ctx, "SELECT id FROM migration_steps WHERE migration_run_id=$1 AND idempotency_key='restore-source:v1'", id).Scan(&stepID); err != nil {
		return migration.Run{}, fmt.Errorf("read source restoration step: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO job_leases (id,migration_run_id,step_id,available_at) VALUES ($1,$2,$3,clock_timestamp())
		ON CONFLICT (migration_run_id,step_id) DO UPDATE SET owner_id='',lease_expires_at=NULL,heartbeat_at=NULL,
		available_at=clock_timestamp(),updated_at=clock_timestamp()`, uuid.New(), id, stepID); err != nil {
		return migration.Run{}, fmt.Errorf("enqueue source restoration: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events
		(migration_run_id,type,severity,message,detail) VALUES ($1,'SOURCE_RESTORE_REQUESTED','WARNING',
		'Source restoration was requested after cutover; target resources are retained','{}')`, id); err != nil {
		return migration.Run{}, fmt.Errorf("record source restoration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return migration.Run{}, fmt.Errorf("commit source restoration: %w", err)
	}
	return r.GetRun(ctx, id)
}

func (r *MigrationRepository) ConfirmCutover(ctx context.Context, runID, administratorID uuid.UUID, checklist []string) error {
	encodedChecklist, err := json.Marshal(checklist)
	if err != nil {
		return fmt.Errorf("marshal cutover checklist: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cutover confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status migration.RunStatus
	var planID uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT status,migration_plan_id FROM migration_runs WHERE id=$1 FOR UPDATE", runID).Scan(&status, &planID); errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock migration run for cutover: %w", err)
	}
	if status != migration.RunAwaitingCutover {
		return repository.ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cutover_confirmations (id,migration_run_id,administrator_id,checklist)
		VALUES ($1,$2,$3,$4) ON CONFLICT (migration_run_id) DO NOTHING`, uuid.New(), runID, administratorID, encodedChecklist); err != nil {
		return fmt.Errorf("record cutover confirmation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_steps SET status='SUCCEEDED',progress=100,completed_at=now(),summary='manual traffic cutover confirmed',updated_at=now()
		WHERE migration_run_id=$1 AND type='AWAIT_CUTOVER' AND status='RUNNING'`, runID); err != nil {
		return fmt.Errorf("complete cutover gate: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_runs SET status='COMPLETED',progress=100,completed_at=now(),updated_at=now() WHERE id=$1`, runID); err != nil {
		return fmt.Errorf("complete migration after cutover: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_plans SET status='COMPLETED',updated_at=now() WHERE id=$1`, planID); err != nil {
		return fmt.Errorf("complete migration plan after cutover: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_events (migration_run_id,type,severity,message,detail)
		VALUES ($1,'CUTOVER_CONFIRMED','INFO','Manual traffic cutover was confirmed',jsonb_build_object('administratorId',$2::text))`, runID, administratorID); err != nil {
		return fmt.Errorf("record cutover event: %w", err)
	}
	return tx.Commit(ctx)
}
