ALTER TABLE mapping_profiles
    ADD COLUMN nfs_mappings jsonb NOT NULL DEFAULT '[]'::jsonb;
