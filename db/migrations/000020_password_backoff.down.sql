BEGIN;
DROP TRIGGER clear_password_backoff ON vault.users;
DROP FUNCTION vault.clear_password_backoff();
DELETE FROM vault.login_attempts WHERE scope='password';
ALTER TABLE vault.login_attempts DROP COLUMN failures, DROP COLUMN lockouts, DROP COLUMN pending, DROP COLUMN blocked_until, DROP COLUMN lease_until, DROP COLUMN generation;
ALTER TABLE vault.login_attempts DROP CONSTRAINT login_attempts_scope_check;
ALTER TABLE vault.login_attempts ADD CONSTRAINT login_attempts_scope_check CHECK (scope IN ('peer','identifier'));
COMMIT;
