BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Cluster-scoped role owned by this vault deployment. Credentials are provisioned
-- outside version control. No writes are needed for the operational foundation.
CREATE ROLE vault_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
    NOINHERIT NOREPLICATION NOBYPASSRLS;
GRANT USAGE ON SCHEMA vault TO vault_runtime;
GRANT SELECT ON vault.users, vault.blobs, vault.files TO vault_runtime;
COMMENT ON ROLE vault_runtime IS 'Vault API runtime; grant future privileges explicitly.';
COMMIT;
