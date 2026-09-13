ALTER TABLE credentials DROP CONSTRAINT credentials_type_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_type_check
    CHECK (type IN ('KUBECONFIG', 'SSH', 'REGISTRY', 'S3', 'COMPOSE_DEFINITION'));

ALTER TABLE source_applications
    ADD COLUMN definition_credential_id uuid REFERENCES credentials(id) ON DELETE RESTRICT;
