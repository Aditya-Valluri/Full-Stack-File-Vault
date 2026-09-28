BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.email_challenges WHERE binding_hash IS NOT NULL AND expires_at>clock_timestamp()) THEN
  RAISE EXCEPTION 'Wait for active verification challenges to expire before rollback';
 END IF;
END $$;
DROP FUNCTION vault.claim_email_attempt(BYTEA);
ALTER TABLE vault.email_challenges DROP COLUMN attempts,DROP COLUMN binding_hash;
COMMIT;
