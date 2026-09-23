-- A failed source restoration requested after a successful cutover is a
-- post-migration warning. It must not overwrite the verified target migration
-- result. Repair records written by older versions accordingly.
UPDATE migration_runs r
SET status = 'COMPLETED',
    progress = 100,
    error_code = 'SOURCE_RESTORE_FAILED',
    updated_at = clock_timestamp()
WHERE r.status = 'FAILED'
  AND EXISTS (
    SELECT 1
    FROM migration_steps s
    WHERE s.migration_run_id = r.id
      AND s.type = 'ROLLBACK'
      AND s.status = 'FAILED'
      AND s.idempotency_key = 'restore-source:v1'
  );

UPDATE migration_plans p
SET status = 'COMPLETED', updated_at = clock_timestamp()
WHERE EXISTS (
  SELECT 1
  FROM migration_runs r
  WHERE r.migration_plan_id = p.id
    AND r.status = 'COMPLETED'
    AND r.error_code = 'SOURCE_RESTORE_FAILED'
);
