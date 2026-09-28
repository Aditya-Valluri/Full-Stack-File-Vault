BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
DROP FUNCTION vault.change_account_password(UUID,BYTEA,TEXT,TEXT,TEXT);
DROP FUNCTION vault.complete_password_reset(BYTEA,BYTEA,TEXT);
DROP FUNCTION vault.claim_password_reset_attempt(BYTEA);
DROP FUNCTION vault.discard_password_reset(BYTEA,BYTEA);
DROP FUNCTION vault.issue_password_reset(BYTEA,BYTEA,TEXT);
DROP TABLE vault.password_reset_challenges;
-- Existing credentials and security versions are preserved.
COMMIT;
