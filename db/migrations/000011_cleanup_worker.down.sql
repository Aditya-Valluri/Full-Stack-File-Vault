BEGIN;
SET LOCAL lock_timeout='5s';
LOCK TABLE vault.object_candidates IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.object_candidates WHERE state='DELETING') THEN
  RAISE EXCEPTION 'cleanup tombstones require explicit recovery before rollback';
 END IF;
END $$;
REVOKE ALL ON vault.files,vault.blobs,vault.object_candidates,vault.file_access FROM vault_gc;
REVOKE UPDATE(state) ON vault.blobs FROM vault_gc;
REVOKE UPDATE(state,next_attempt_at) ON vault.object_candidates FROM vault_gc;
REVOKE USAGE ON SCHEMA vault FROM vault_gc;
DROP ROLE vault_gc;
DROP INDEX vault.object_candidates_retry_idx;
ALTER TABLE vault.object_candidates DROP COLUMN next_attempt_at;
COMMIT;
