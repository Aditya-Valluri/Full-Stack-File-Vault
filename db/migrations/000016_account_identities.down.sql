BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vault.user_identities)
    OR EXISTS (SELECT 1 FROM vault.users WHERE email_address IS NOT NULL OR email_normalized IS NOT NULL OR email_verified_at IS NOT NULL) THEN
  RAISE EXCEPTION 'Refusing to discard account identities or verified emails';
 END IF;
END $$;
DROP TABLE vault.user_identities;
DROP INDEX vault.users_verified_email_unique;
ALTER TABLE vault.users DROP CONSTRAINT users_verified_email_complete,
 DROP COLUMN email_address, DROP COLUMN email_normalized, DROP COLUMN email_verified_at;
COMMIT;
