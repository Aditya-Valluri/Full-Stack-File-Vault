BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
-- Exclude file writers while deriving the initial counter. Existing over-quota
-- data aborts the migration rather than being silently deleted or clamped.
LOCK TABLE vault.users, vault.files, vault.blobs IN ACCESS EXCLUSIVE MODE;
ALTER TABLE vault.users ADD COLUMN used_bytes BIGINT NOT NULL DEFAULT 0;
UPDATE vault.users u SET used_bytes = totals.bytes
FROM (SELECT f.owner_id, SUM(b.size_bytes)::bigint AS bytes
      FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id GROUP BY f.owner_id) totals
WHERE u.id=totals.owner_id;
ALTER TABLE vault.users ADD CONSTRAINT users_usage_within_quota
 CHECK (used_bytes >= 0 AND used_bytes <= quota_bytes);
ALTER TABLE vault.blobs
 ADD COLUMN state TEXT NOT NULL DEFAULT 'ACTIVE',
 ADD COLUMN gc_after TIMESTAMPTZ,
 ADD CONSTRAINT blobs_lifecycle CHECK
 ((state='ACTIVE' AND gc_after IS NULL) OR (state='GC_PENDING' AND gc_after IS NOT NULL));
CREATE TABLE vault.object_candidates (
 storage_key TEXT PRIMARY KEY CHECK (char_length(btrim(storage_key)) BETWEEN 1 AND 1024),
 sha256 BYTEA NOT NULL CHECK (octet_length(sha256)=32),
 size_bytes BIGINT NOT NULL CHECK (size_bytes>=0),
 state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','PUBLISHED','CLEANUP','DELETING')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX object_candidates_cleanup_idx ON vault.object_candidates(state,created_at)
 WHERE state IN ('PENDING','CLEANUP','DELETING');
GRANT UPDATE(used_bytes) ON vault.users TO vault_runtime;
GRANT INSERT ON vault.files, vault.blobs TO vault_runtime;
GRANT UPDATE(storage_key,state,gc_after) ON vault.blobs TO vault_runtime;
GRANT SELECT,INSERT ON vault.object_candidates TO vault_runtime;
GRANT UPDATE(state) ON vault.object_candidates TO vault_runtime;
COMMENT ON TABLE vault.object_candidates IS 'Durable final-generation intent. Uncertain publication outcomes must be reconciled before deleting physical content.';
COMMIT;
