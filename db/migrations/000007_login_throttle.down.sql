BEGIN;
SET LOCAL lock_timeout = '5s';
DROP TABLE vault.login_attempts;
DROP TABLE vault.login_budget;
COMMIT;
