-- Restoring a source after a successful cutover does not invalidate the
-- completed migration. Repair runs written by older versions accordingly.
UPDATE migration_runs r
SET status = 'COMPLETED',
    error_code = 'SOURCE_RESTORED',
    error_message = 'Source workload restored; target resources and migration evidence are retained',
    updated_at = clock_timestamp()
WHERE r.status = 'CANCELLED'
  AND r.error_code = 'SOURCE_RESTORE_REQUESTED'
  AND EXISTS (
    SELECT 1 FROM migration_events e
    WHERE e.migration_run_id = r.id AND e.type = 'SOURCE_RESTORE_COMPLETED'
  );

UPDATE migration_plans p
SET status = 'COMPLETED', updated_at = clock_timestamp()
WHERE EXISTS (
  SELECT 1 FROM migration_runs r
  WHERE r.migration_plan_id = p.id
    AND r.status = 'COMPLETED'
    AND r.error_code = 'SOURCE_RESTORED'
);
