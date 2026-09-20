BEGIN;
SET LOCAL lock_timeout='5s';
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.file_tags) THEN
  RAISE EXCEPTION 'Refusing to discard private tags';
 END IF;
END $$;
DROP TABLE vault.file_tags;
COMMIT;
