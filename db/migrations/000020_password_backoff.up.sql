BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
ALTER TABLE vault.login_attempts DROP CONSTRAINT login_attempts_scope_check;
ALTER TABLE vault.login_attempts ADD CONSTRAINT login_attempts_scope_check CHECK (scope IN ('peer','identifier','password'));
ALTER TABLE vault.login_attempts
 ADD COLUMN failures INTEGER NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 5),
 ADD COLUMN lockouts INTEGER NOT NULL DEFAULT 0 CHECK (lockouts BETWEEN 0 AND 4),
 ADD COLUMN pending INTEGER NOT NULL DEFAULT 0 CHECK (pending BETWEEN 0 AND 5),
 ADD COLUMN blocked_until TIMESTAMPTZ,
 ADD COLUMN lease_until TIMESTAMPTZ,
 ADD COLUMN generation TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();
GRANT UPDATE(failures,lockouts,pending,blocked_until,lease_until,generation) ON vault.login_attempts TO vault_runtime;
CREATE FUNCTION vault.clear_password_backoff() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,vault AS $$
BEGIN
 IF NEW.auth_version IS DISTINCT FROM OLD.auth_version THEN
  DELETE FROM vault.login_attempts WHERE scope='password' AND key_hash IN (
   SELECT sha256(convert_to(NEW.email_normalized,'UTF8')) WHERE NEW.email_normalized IS NOT NULL
   UNION ALL SELECT sha256(convert_to(login_name,'UTF8')) FROM vault.credentials WHERE user_id=NEW.id
  );
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION vault.clear_password_backoff() FROM PUBLIC;
CREATE TRIGGER clear_password_backoff AFTER UPDATE OF auth_version ON vault.users
FOR EACH ROW EXECUTE FUNCTION vault.clear_password_backoff();
COMMIT;
