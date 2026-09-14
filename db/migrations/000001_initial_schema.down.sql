BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Reverse dependency order.
-- Deliberately avoid CASCADE so unexpected dependencies fail loudly.

DROP TABLE vault.files;
DROP TABLE vault.blobs;
DROP TABLE vault.users;

DROP SCHEMA vault;

COMMIT;
