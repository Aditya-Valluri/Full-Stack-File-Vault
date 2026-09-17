BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE vault.users
    ADD COLUMN role TEXT NOT NULL DEFAULT 'USER' CHECK (role IN ('USER', 'ADMIN')),
    ADD COLUMN disabled_at TIMESTAMPTZ;
-- Existing users remain valid but cannot authenticate without credentials.
CREATE TABLE vault.credentials (
    user_id UUID PRIMARY KEY REFERENCES vault.users(id) ON DELETE RESTRICT,
    login_name TEXT COLLATE "C" NOT NULL UNIQUE
        CHECK (login_name ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    password_hash TEXT NOT NULL CHECK (octet_length(password_hash) BETWEEN 80 AND 512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
GRANT SELECT ON vault.credentials TO vault_runtime;
COMMENT ON TABLE vault.credentials IS 'Operator-provisioned credentials; no runtime writes or public registration.';
COMMIT;
