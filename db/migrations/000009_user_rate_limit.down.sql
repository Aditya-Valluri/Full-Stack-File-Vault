BEGIN;
SET LOCAL lock_timeout = '5s';
DROP TABLE vault.user_request_windows;
COMMIT;
