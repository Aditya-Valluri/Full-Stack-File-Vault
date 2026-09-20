BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE vault.users, vault.files, vault.blobs, vault.object_candidates IN ACCESS EXCLUSIVE MODE;
-- Do not discard recovery records or disable quota beneath existing files.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.files) OR EXISTS(SELECT 1 FROM vault.blobs)
 OR EXISTS(SELECT 1 FROM vault.object_candidates) THEN
  RAISE EXCEPTION 'file publication rollback requires an empty vault and resolved candidates';
 END IF;
END $$;
REVOKE INSERT ON vault.files,vault.blobs FROM vault_runtime;
REVOKE UPDATE(storage_key,state,gc_after) ON vault.blobs FROM vault_runtime;
REVOKE UPDATE(used_bytes) ON vault.users FROM vault_runtime;
DROP TABLE vault.object_candidates;
ALTER TABLE vault.blobs DROP CONSTRAINT blobs_lifecycle, DROP COLUMN state, DROP COLUMN gc_after;
ALTER TABLE vault.users DROP CONSTRAINT users_usage_within_quota, DROP COLUMN used_bytes;
COMMIT;
