-- Mapping profiles are an optional set of overrides. Without one, Compose
-- creates a namespace from the project name and Kubernetes keeps the source
-- namespace/resource values, using target defaults where appropriate.
ALTER TABLE migration_plans
    ALTER COLUMN mapping_profile_id DROP NOT NULL;
