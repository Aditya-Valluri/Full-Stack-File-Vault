BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
REVOKE SELECT ON vault.users, vault.blobs, vault.files FROM vault_runtime;
REVOKE USAGE ON SCHEMA vault FROM vault_runtime;
-- Fails if unexpected grants or dependencies exist; do not use DROP OWNED CASCADE.
DROP ROLE vault_runtime;
COMMIT;
