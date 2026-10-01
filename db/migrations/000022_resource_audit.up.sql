BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
ALTER TABLE vault.admin_audit ALTER COLUMN actor_id DROP NOT NULL, ALTER COLUMN target_user_id DROP NOT NULL,
 ALTER COLUMN previous_quota SET DEFAULT 0, ALTER COLUMN new_quota SET DEFAULT 0,
 ADD COLUMN file_id UUID, ADD COLUMN share_id UUID;
ALTER TABLE vault.admin_audit DROP CONSTRAINT admin_audit_action_check;
ALTER TABLE vault.admin_audit ADD CONSTRAINT admin_audit_action_check CHECK(action IN (
 'USER_QUOTA_CHANGED','USER_DISABLED','USER_ENABLED','AUTHENTICATED_SESSION_CREATED',
 'FILE_UPLOADED','FILE_DELETED','SHARE_CREATED','SHARE_REMOVED','SHARE_OPENED',
 'SHARED_DOWNLOAD_STARTED','SECURITY_VERSION_CHANGED','LOGIN_REJECTED','DOWNLOAD_STARTED'));
-- Snapshot UUIDs intentionally have no file/share FK: deletion must not erase evidence.
CREATE FUNCTION vault.record_resource_audit() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,vault AS $$
DECLARE account UUID; actor UUID; file UUID; share UUID; event TEXT;
BEGIN
 IF TG_TABLE_NAME='sessions' THEN
  IF NEW.user_id IS NULL THEN RETURN NEW; END IF;
  account:=NEW.user_id; actor:=account; event:='AUTHENTICATED_SESSION_CREATED';
 ELSIF TG_TABLE_NAME='files' THEN
  IF TG_OP='INSERT' THEN
   account:=NEW.owner_id; actor:=account; file:=NEW.id; event:='FILE_UPLOADED';
  ELSE
   account:=OLD.owner_id; file:=OLD.id; event:='FILE_DELETED';
  END IF;
 ELSIF TG_TABLE_NAME='file_shares' THEN
  IF TG_OP='INSERT' THEN
   account:=NEW.owner_id; actor:=account; file:=NEW.file_id; share:=NEW.id; event:='SHARE_CREATED';
  ELSE
   account:=OLD.owner_id; file:=OLD.file_id; share:=OLD.id; event:='SHARE_REMOVED';
  END IF;
 ELSIF TG_TABLE_NAME='share_activity' THEN
  SELECT owner_id INTO account FROM vault.files WHERE id=NEW.file_id;
  actor:=NEW.recipient_id; file:=NEW.file_id; share:=NEW.share_id;
  event:=CASE NEW.kind WHEN 'OPENED' THEN 'SHARE_OPENED' ELSE 'SHARED_DOWNLOAD_STARTED' END;
 ELSIF TG_TABLE_NAME='login_attempts' THEN
  IF NEW.scope<>'password' OR NEW.failures<=OLD.failures THEN RETURN NEW; END IF;
  event:='LOGIN_REJECTED';
 ELSIF TG_TABLE_NAME='file_access' THEN
  IF NOT NEW.download_counted OR OLD.download_counted THEN RETURN NEW; END IF;
  account:=NEW.owner_id; actor:=account; file:=NEW.file_id; event:='DOWNLOAD_STARTED';
 ELSIF TG_TABLE_NAME='users' THEN
  IF NEW.auth_version IS NOT DISTINCT FROM OLD.auth_version THEN RETURN NEW; END IF;
  account:=NEW.id; event:='SECURITY_VERSION_CHANGED';
 END IF;
 INSERT INTO vault.admin_audit(actor_id,target_user_id,action,file_id,share_id)
 VALUES(actor,account,event,file,share);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION vault.record_resource_audit() FROM PUBLIC;
ALTER TABLE vault.file_access ADD COLUMN download_counted BOOLEAN NOT NULL DEFAULT false;
GRANT UPDATE(download_counted) ON vault.file_access TO vault_runtime;
CREATE TRIGGER resource_audit AFTER UPDATE OF download_counted ON vault.file_access FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER UPDATE OF failures ON vault.login_attempts FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER INSERT ON vault.sessions FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER INSERT OR DELETE ON vault.files FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER INSERT OR DELETE ON vault.file_shares FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER INSERT ON vault.share_activity FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
CREATE TRIGGER resource_audit AFTER UPDATE OF auth_version ON vault.users FOR EACH ROW EXECUTE FUNCTION vault.record_resource_audit();
COMMENT ON TABLE vault.admin_audit IS 'Append-only runtime audit. Non-administrative events use zero quota placeholders. Unknown/system actors remain NULL. Resource IDs survive deletion; no credentials or raw capability values.';
COMMIT;
