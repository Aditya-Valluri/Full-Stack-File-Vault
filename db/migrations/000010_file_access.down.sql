BEGIN;
SET LOCAL lock_timeout='5s';
LOCK TABLE vault.file_access IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.file_access) THEN
  RAISE EXCEPTION 'expire and remove access grants before rollback';
 END IF;
END $$;
DROP TABLE vault.file_access;
DROP INDEX vault.blobs_gc_due_idx;
REVOKE DELETE ON vault.files FROM vault_runtime;
COMMIT;
