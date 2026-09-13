CREATE TABLE workload_replica_snapshots (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    namespace text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('Deployment', 'StatefulSet')),
    name text NOT NULL,
    replicas integer NOT NULL CHECK (replicas >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(migration_run_id, namespace, kind, name)
);

CREATE INDEX workload_replica_snapshots_run_idx
    ON workload_replica_snapshots(migration_run_id, namespace, kind, name);
