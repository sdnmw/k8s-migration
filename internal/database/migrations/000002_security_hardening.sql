ALTER TABLE administrators
    ADD COLUMN singleton boolean NOT NULL DEFAULT true CHECK (singleton);

CREATE UNIQUE INDEX administrators_singleton_idx ON administrators(singleton);
CREATE INDEX sessions_token_expiry_idx ON sessions(token_hash, expires_at);
