BEGIN;
ALTER TABLE vault.file_shares DROP CONSTRAINT file_shares_permission_check;
ALTER TABLE vault.file_shares ADD CONSTRAINT file_shares_permission_check
 CHECK(permission IN ('DOWNLOAD','PREVIEW_AND_DOWNLOAD','PREVIEW_ONLY'));
COMMIT;
