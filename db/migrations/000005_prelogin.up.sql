BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE vault.users ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 0 CHECK (auth_version >= 0);
-- Column-scoped UPDATE also permits row locks, without granting role/status changes.
GRANT UPDATE(auth_version) ON vault.users TO vault_runtime;
ALTER TABLE vault.sessions ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE vault.sessions ADD CONSTRAINT sessions_anonymous_lifetime
    CHECK (user_id IS NOT NULL OR expires_at <= created_at + interval '10 minutes');
COMMENT ON TABLE vault.sessions IS 'Anonymous or authenticated sessions. Anonymous rows are consumed by revocation, never upgraded in place.';
COMMIT;
