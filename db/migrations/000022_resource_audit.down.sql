BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
DROP TRIGGER resource_audit ON vault.sessions;
DROP TRIGGER resource_audit ON vault.files;
DROP TRIGGER resource_audit ON vault.file_shares;
DROP TRIGGER resource_audit ON vault.share_activity;
DROP TRIGGER resource_audit ON vault.users;
DROP TRIGGER resource_audit ON vault.login_attempts;
DROP TRIGGER resource_audit ON vault.file_access;
ALTER TABLE vault.file_access DROP COLUMN download_counted;
DROP FUNCTION vault.record_resource_audit();
-- Rollback discards only event kinds unsupported by the previous schema.
DELETE FROM vault.admin_audit WHERE action NOT IN ('USER_QUOTA_CHANGED','USER_DISABLED','USER_ENABLED');
ALTER TABLE vault.admin_audit DROP CONSTRAINT admin_audit_action_check;
ALTER TABLE vault.admin_audit ADD CONSTRAINT admin_audit_action_check CHECK(action IN ('USER_QUOTA_CHANGED','USER_DISABLED','USER_ENABLED'));
ALTER TABLE vault.admin_audit ALTER COLUMN actor_id SET NOT NULL, ALTER COLUMN target_user_id SET NOT NULL,
 ALTER COLUMN previous_quota DROP DEFAULT, ALTER COLUMN new_quota DROP DEFAULT,
 DROP COLUMN file_id, DROP COLUMN share_id;
COMMIT;
