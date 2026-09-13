CREATE TABLE administrators (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    administrator_id uuid NOT NULL REFERENCES administrators(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_administrator_idx ON sessions(administrator_id);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);

CREATE TABLE credentials (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('KUBECONFIG', 'SSH', 'REGISTRY', 'S3')),
    encrypted_payload bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE environments (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    role text NOT NULL CHECK (role IN ('SOURCE', 'TARGET')),
    kind text NOT NULL CHECK (kind IN ('KUBERNETES', 'DOCKER_COMPOSE')),
    endpoint text NOT NULL DEFAULT '',
    credential_id uuid REFERENCES credentials(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('PENDING', 'CONNECTED', 'DISCONNECTED', 'ERROR')),
    status_message text NOT NULL DEFAULT '',
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    capabilities_updated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (role <> 'TARGET' OR kind = 'KUBERNETES')
);

CREATE TABLE storage_profiles (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('SMTX_BLOCK', 'EXISTING_NFS_SC', 'EXTERNAL_NFS_SC')),
    storage_class_name text NOT NULL,
    provisioner text NOT NULL DEFAULT '',
    nfs_server text NOT NULL DEFAULT '',
    nfs_export text NOT NULL DEFAULT '',
    mount_options text[] NOT NULL DEFAULT '{}',
    reclaim_policy text NOT NULL DEFAULT 'Retain' CHECK (reclaim_policy IN ('Retain', 'Delete')),
    status text NOT NULL DEFAULT 'PENDING',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(environment_id, name),
    UNIQUE(environment_id, storage_class_name),
    CHECK (type <> 'EXTERNAL_NFS_SC' OR (nfs_server <> '' AND nfs_export <> ''))
);

CREATE TABLE object_storage_profiles (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    endpoint text NOT NULL,
    bucket text NOT NULL,
    region text NOT NULL DEFAULT 'minio',
    credential_id uuid NOT NULL REFERENCES credentials(id) ON DELETE RESTRICT,
    tls_verify boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE addon_installations (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('MINIO', 'VELERO', 'NFS_CSI')),
    version text NOT NULL,
    status text NOT NULL CHECK (status IN ('PENDING', 'INSTALLING', 'READY', 'FAILED')),
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(environment_id, type)
);

CREATE TABLE source_applications (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name text NOT NULL,
    source_type text NOT NULL CHECK (source_type IN ('KUBERNETES', 'COMPOSE')),
    namespace text NOT NULL DEFAULT '',
    inventory jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(environment_id, name, namespace)
);

CREATE TABLE assessments (
    id uuid PRIMARY KEY,
    application_id uuid NOT NULL REFERENCES source_applications(id) ON DELETE CASCADE,
    score integer NOT NULL DEFAULT 0 CHECK (score BETWEEN 0 AND 100),
    blocker_count integer NOT NULL DEFAULT 0 CHECK (blocker_count >= 0),
    warning_count integer NOT NULL DEFAULT 0 CHECK (warning_count >= 0),
    info_count integer NOT NULL DEFAULT 0 CHECK (info_count >= 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE TABLE assessment_issues (
    id uuid PRIMARY KEY,
    assessment_id uuid NOT NULL REFERENCES assessments(id) ON DELETE CASCADE,
    severity text NOT NULL CHECK (severity IN ('BLOCKER', 'WARNING', 'INFO')),
    category text NOT NULL CHECK (category IN ('COMPUTE', 'STORAGE', 'NETWORK', 'SECURITY', 'IMAGE', 'API', 'DEPENDENCY')),
    resource_kind text NOT NULL,
    resource_namespace text NOT NULL DEFAULT '',
    resource_name text NOT NULL,
    rule_id text NOT NULL,
    title text NOT NULL,
    description text NOT NULL,
    remediation text NOT NULL DEFAULT '',
    auto_fixable boolean NOT NULL DEFAULT false,
    UNIQUE(assessment_id, rule_id, resource_kind, resource_namespace, resource_name)
);
CREATE INDEX assessment_issues_assessment_idx ON assessment_issues(assessment_id, severity);

CREATE TABLE mapping_profiles (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    target_environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    storage_mappings jsonb NOT NULL DEFAULT '[]'::jsonb,
    namespace_mappings jsonb NOT NULL DEFAULT '[]'::jsonb,
    ingress_mappings jsonb NOT NULL DEFAULT '[]'::jsonb,
    registry_mappings jsonb NOT NULL DEFAULT '[]'::jsonb,
    node_label_mappings jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(target_environment_id, name)
);

CREATE TABLE migration_plans (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    source_environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE RESTRICT,
    target_environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE RESTRICT,
    source_application_id uuid NOT NULL REFERENCES source_applications(id) ON DELETE RESTRICT,
    assessment_id uuid NOT NULL REFERENCES assessments(id) ON DELETE RESTRICT,
    mapping_profile_id uuid NOT NULL REFERENCES mapping_profiles(id) ON DELETE RESTRICT,
    strategy jsonb NOT NULL,
    validation_policy jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('DRAFT', 'READY', 'BLOCKED', 'RUNNING', 'COMPLETED', 'FAILED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (source_environment_id <> target_environment_id)
);

CREATE TABLE migration_runs (
    id uuid PRIMARY KEY,
    migration_plan_id uuid NOT NULL REFERENCES migration_plans(id) ON DELETE CASCADE,
    run_number integer NOT NULL CHECK (run_number > 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'PREFLIGHT', 'PRESYNC', 'QUIESCE', 'FINAL_BACKUP', 'TRANSFER', 'TRANSFORM', 'RESTORE', 'VALIDATION', 'AWAITING_CUTOVER', 'ROLLING_BACK', 'COMPLETED', 'FAILED', 'CANCELLED')),
    progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    bytes_total bigint NOT NULL DEFAULT 0 CHECK (bytes_total >= 0),
    bytes_transferred bigint NOT NULL DEFAULT 0 CHECK (bytes_transferred >= 0),
    started_at timestamptz,
    completed_at timestamptz,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(migration_plan_id, run_number),
    CHECK (bytes_transferred <= bytes_total OR bytes_total = 0)
);

CREATE TABLE migration_steps (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    type text NOT NULL,
    attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'SKIPPED')),
    progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    started_at timestamptz,
    completed_at timestamptz,
    summary text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(migration_run_id, idempotency_key)
);

CREATE TABLE volume_transfers (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    engine text NOT NULL CHECK (engine IN ('VELERO_FSB', 'CSI_DATA_MOVER', 'COMPOSE_KOPIA')),
    namespace text NOT NULL DEFAULT '',
    source_volume text NOT NULL,
    target_volume text NOT NULL,
    total_bytes bigint NOT NULL DEFAULT 0 CHECK (total_bytes >= 0),
    transferred_bytes bigint NOT NULL DEFAULT 0 CHECK (transferred_bytes >= 0),
    throughput_bytes_per_second bigint NOT NULL DEFAULT 0 CHECK (throughput_bytes_per_second >= 0),
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    checksum_status text NOT NULL DEFAULT 'PENDING',
    status text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')),
    error_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(migration_run_id, namespace, source_volume)
);

CREATE TABLE validation_results (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    category text NOT NULL CHECK (category IN ('WORKLOAD', 'STORAGE', 'NETWORK', 'APPLICATION')),
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('PASSED', 'FAILED', 'WARNING')),
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cutover_confirmations (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL UNIQUE REFERENCES migration_runs(id) ON DELETE CASCADE,
    administrator_id uuid NOT NULL REFERENCES administrators(id) ON DELETE RESTRICT,
    checklist jsonb NOT NULL DEFAULT '[]'::jsonb,
    confirmed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE migration_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    type text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('INFO', 'WARNING', 'ERROR')),
    message text NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX migration_events_stream_idx ON migration_events(migration_run_id, id);

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor text NOT NULL,
    action text NOT NULL,
    object_type text NOT NULL,
    object_id uuid,
    result text NOT NULL CHECK (result IN ('SUCCESS', 'FAILURE')),
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_created_idx ON audit_events(created_at DESC);

CREATE TABLE job_leases (
    id uuid PRIMARY KEY,
    migration_run_id uuid NOT NULL REFERENCES migration_runs(id) ON DELETE CASCADE,
    step_id uuid REFERENCES migration_steps(id) ON DELETE CASCADE,
    owner_id text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    heartbeat_at timestamptz,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(migration_run_id, step_id)
);
CREATE INDEX job_leases_claim_idx ON job_leases(available_at, lease_expires_at);

