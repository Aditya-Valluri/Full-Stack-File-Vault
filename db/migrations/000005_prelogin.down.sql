BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
-- Refuse rollback while anonymous records exist; do not silently destroy sessions.
ALTER TABLE vault.sessions ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE vault.sessions DROP CONSTRAINT sessions_anonymous_lifetime;
REVOKE UPDATE(auth_version) ON vault.users FROM vault_runtime;
ALTER TABLE vault.users DROP COLUMN auth_version;
COMMIT;
