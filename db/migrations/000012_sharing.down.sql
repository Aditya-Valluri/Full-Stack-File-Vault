BEGIN;
SET LOCAL lock_timeout='5s';
LOCK TABLE vault.file_shares,vault.shared_access,vault.shared_request_windows,vault.file_download_counts IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vault.file_shares) OR EXISTS(SELECT 1 FROM vault.shared_access)
 OR EXISTS(SELECT 1 FROM vault.file_download_counts WHERE shared_downloads>0) THEN
  RAISE EXCEPTION 'sharing records and download history require explicit recovery before rollback';
 END IF;
END $$;
DROP TABLE vault.shared_access,vault.shared_request_windows,vault.file_download_counts,vault.file_shares;
COMMIT;
