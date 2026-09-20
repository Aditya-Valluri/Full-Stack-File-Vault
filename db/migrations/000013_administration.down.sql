BEGIN;
SET LOCAL lock_timeout='5s';
LOCK TABLE vault.admin_audit IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.admin_audit) THEN
  RAISE EXCEPTION 'audit records require explicit preservation before rollback';
 END IF;
END $$;
REVOKE UPDATE(quota_bytes,disabled_at) ON vault.users FROM vault_runtime;
DROP TABLE vault.admin_audit;
COMMIT;
