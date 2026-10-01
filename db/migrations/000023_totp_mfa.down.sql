BEGIN;
SET LOCAL lock_timeout='5s';
-- Removing enrolled MFA would weaken account security. Require explicit prior
-- operator recovery/unenrollment; never silently turn two-factor accounts off.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.user_mfa WHERE enabled) THEN
  RAISE EXCEPTION 'Cannot roll back MFA while enabled enrollments exist';
 END IF;
END $$;
DROP TABLE vault.mfa_recovery_codes;
DROP TABLE vault.user_mfa;
COMMIT;
