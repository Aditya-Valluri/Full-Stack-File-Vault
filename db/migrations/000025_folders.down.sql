BEGIN;
-- Files remain owned and accessible at root when organization is rolled back.
ALTER TABLE vault.files DROP COLUMN folder_id;
DROP TABLE vault.folders;
COMMIT;
