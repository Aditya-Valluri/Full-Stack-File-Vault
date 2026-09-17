BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
CREATE TABLE vault.sessions (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE RESTRICT,
    csrf_token TEXT NOT NULL CHECK (csrf_token ~ '^[A-Za-z0-9_-]{43}$'),
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    idle_expires_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (created_at <= last_seen_at AND last_seen_at < idle_expires_at
           AND idle_expires_at <= expires_at)
);
CREATE INDEX sessions_user_id_idx ON vault.sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON vault.sessions(expires_at);
GRANT SELECT, INSERT ON vault.sessions TO vault_runtime;
GRANT UPDATE(last_seen_at, idle_expires_at, revoked_at) ON vault.sessions TO vault_runtime;
COMMENT ON TABLE vault.sessions IS 'Authenticated server sessions. Raw authentication tokens are never persisted. Browser integration is separate.';
COMMIT;
