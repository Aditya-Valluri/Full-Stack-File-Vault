BEGIN;
SET LOCAL lock_timeout='5s';
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.upload_receipts) THEN
  RAISE EXCEPTION 'refusing to discard upload retry protection';
 END IF;
END $$;
DROP TABLE vault.upload_receipts;
COMMIT;
