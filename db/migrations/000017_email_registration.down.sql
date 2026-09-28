BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
-- Verified users and their identities are deliberately preserved.
DROP FUNCTION vault.complete_email_registration(BYTEA,TEXT);
DROP TABLE vault.email_challenges;
COMMIT;
