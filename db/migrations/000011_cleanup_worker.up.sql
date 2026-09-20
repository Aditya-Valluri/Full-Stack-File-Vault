BEGIN;
SET LOCAL lock_timeout='5s';
ALTER TABLE vault.object_candidates ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();
CREATE INDEX object_candidates_retry_idx ON vault.object_candidates(next_attempt_at,storage_key)
 WHERE state IN ('PENDING','CLEANUP','DELETING');
CREATE ROLE vault_gc NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
GRANT USAGE ON SCHEMA vault TO vault_gc;
GRANT SELECT ON vault.files,vault.blobs,vault.object_candidates,vault.file_access TO vault_gc;
GRANT DELETE ON vault.blobs,vault.file_access TO vault_gc;
-- Row locking requires UPDATE privilege on at least one column.
GRANT UPDATE(state) ON vault.blobs TO vault_gc;
GRANT INSERT ON vault.object_candidates TO vault_gc;
GRANT UPDATE(state,next_attempt_at) ON vault.object_candidates TO vault_gc;
COMMENT ON COLUMN vault.object_candidates.next_attempt_at IS 'Retry schedule, never evidence that a live writer has stopped. DELETING tombstones are retained for late-I/O recovery.';
COMMIT;
