BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
DROP TABLE vault.credentials;
ALTER TABLE vault.users DROP COLUMN disabled_at, DROP COLUMN role;
COMMIT;
