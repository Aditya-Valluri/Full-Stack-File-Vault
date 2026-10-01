BEGIN;
-- Never turn a view-only link into a download link during rollback.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.file_shares WHERE permission='PREVIEW_ONLY') THEN
  RAISE EXCEPTION 'Revoke view-only shares before rolling back this migration';
 END IF;
END $$;
ALTER TABLE vault.file_shares DROP CONSTRAINT file_shares_permission_check;
ALTER TABLE vault.file_shares ADD CONSTRAINT file_shares_permission_check
 CHECK(permission IN ('DOWNLOAD','PREVIEW_AND_DOWNLOAD'));
COMMIT;
