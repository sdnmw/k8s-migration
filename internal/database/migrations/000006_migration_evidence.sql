CREATE TABLE migration_run_topologies (
    migration_run_id uuid PRIMARY KEY REFERENCES migration_runs(id) ON DELETE CASCADE,
    evidence jsonb NOT NULL,
    current_observation jsonb,
    terminal_snapshot boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE migration_step_attempts (
    step_id uuid NOT NULL REFERENCES migration_steps(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    status text NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'RETRY_SCHEDULED', 'FAILED')),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    heartbeat_at timestamptz,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    diagnostic text NOT NULL DEFAULT '',
    PRIMARY KEY(step_id, attempt)
);
CREATE INDEX migration_step_attempts_step_idx ON migration_step_attempts(step_id, attempt);

ALTER TABLE validation_results
    ADD COLUMN side text NOT NULL DEFAULT 'TARGET' CHECK (side IN ('SOURCE', 'TARGET')),
    ADD COLUMN resource_kind text NOT NULL DEFAULT '',
    ADD COLUMN resource_namespace text NOT NULL DEFAULT '',
    ADD COLUMN resource_name text NOT NULL DEFAULT '',
    ADD COLUMN error_code text NOT NULL DEFAULT '',
    ADD COLUMN detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN observed_at timestamptz NOT NULL DEFAULT now();
