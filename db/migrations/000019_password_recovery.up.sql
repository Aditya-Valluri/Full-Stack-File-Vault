BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
CREATE TABLE vault.password_reset_challenges(
 token_hash BYTEA PRIMARY KEY CHECK(octet_length(token_hash)=32),
 binding_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(binding_hash)=32),
 user_id UUID REFERENCES vault.users(id) ON DELETE RESTRICT,
 auth_version BIGINT,
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT(CURRENT_TIMESTAMP+interval '15 minutes'),
 CHECK((user_id IS NULL)=(auth_version IS NULL))
);
CREATE INDEX password_reset_expiry ON vault.password_reset_challenges(expires_at);
CREATE INDEX password_reset_user ON vault.password_reset_challenges(user_id);
-- Runtime gets no table privileges. Every transition has a dedicated function.
CREATE FUNCTION vault.issue_password_reset(code_hash BYTEA,browser_hash BYTEA,email TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
DECLARE account UUID; version BIGINT; locked_session BYTEA;
BEGIN
 IF octet_length(code_hash)<>32 OR octet_length(browser_hash)<>32 OR code_hash IS NULL OR browser_hash IS NULL OR email IS NULL OR octet_length(email) NOT BETWEEN 3 AND 254 THEN RETURN false; END IF;
 SELECT token_hash INTO locked_session FROM vault.sessions WHERE token_hash=browser_hash
 AND user_id IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp() FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT u.id,u.auth_version INTO account,version FROM vault.users u JOIN vault.user_identities i ON i.user_id=u.id AND i.provider='password'
 WHERE u.email_normalized=email AND u.disabled_at IS NULL;
 DELETE FROM vault.password_reset_challenges WHERE expires_at<=clock_timestamp() OR binding_hash=browser_hash;
 INSERT INTO vault.password_reset_challenges(token_hash,binding_hash,user_id,auth_version) VALUES(code_hash,browser_hash,account,version);
 RETURN true;
END $$;
CREATE FUNCTION vault.discard_password_reset(code_hash BYTEA,browser_hash BYTEA)
RETURNS VOID LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
 DELETE FROM vault.password_reset_challenges WHERE token_hash=code_hash AND binding_hash=browser_hash;
$$;
CREATE FUNCTION vault.claim_password_reset_attempt(browser_hash BYTEA)
RETURNS BYTEA LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
 UPDATE vault.password_reset_challenges SET attempts=attempts+1
 WHERE binding_hash=browser_hash AND attempts<5 AND expires_at>clock_timestamp()
 RETURNING token_hash;
$$;
-- Lock order matches login/admin: user, then session. Password changes racing
-- credential verification invalidate the auth_version checked by login.
CREATE FUNCTION vault.complete_password_reset(code_hash BYTEA,browser_hash BYTEA,new_hash TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
DECLARE account UUID; version BIGINT; locked_session BYTEA;
BEGIN
 IF new_hash IS NULL OR new_hash !~ '^[$]argon2id[$]v=19[$]m=19456,t=2,p=1[$][A-Za-z0-9+/]{22}[$][A-Za-z0-9+/]{43}$' THEN RETURN false; END IF;
 SELECT user_id INTO account FROM vault.password_reset_challenges WHERE token_hash=code_hash AND binding_hash=browser_hash AND expires_at>clock_timestamp();
 IF account IS NULL THEN RETURN false; END IF;
 SELECT auth_version INTO version FROM vault.users WHERE id=account AND disabled_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT token_hash INTO locked_session FROM vault.sessions WHERE token_hash=browser_hash AND user_id IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp() FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 IF NOT EXISTS(SELECT 1 FROM vault.user_identities WHERE user_id=account AND provider='password') THEN RETURN false; END IF;
 DELETE FROM vault.password_reset_challenges WHERE token_hash=code_hash AND binding_hash=browser_hash AND user_id=account AND auth_version=version AND expires_at>clock_timestamp();
 IF NOT FOUND THEN RETURN false; END IF;
 UPDATE vault.user_identities SET password_hash=new_hash WHERE user_id=account AND provider='password';
 UPDATE vault.users SET auth_version=auth_version+1 WHERE id=account;
 UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE (user_id=account OR token_hash=browser_hash) AND revoked_at IS NULL;
 DELETE FROM vault.password_reset_challenges WHERE user_id=account;
 RETURN true;
END $$;
CREATE FUNCTION vault.change_account_password(account UUID,browser_hash BYTEA,method TEXT,old_hash TEXT,new_hash TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
DECLARE locked_user UUID; locked_session BYTEA;
BEGIN
 IF new_hash IS NULL OR new_hash !~ '^[$]argon2id[$]v=19[$]m=19456,t=2,p=1[$][A-Za-z0-9+/]{22}[$][A-Za-z0-9+/]{43}$' THEN RETURN false; END IF;
 SELECT id INTO locked_user FROM vault.users WHERE id=account AND disabled_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT token_hash INTO locked_session FROM vault.sessions WHERE token_hash=browser_hash AND user_id=account AND revoked_at IS NULL AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp() FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 IF method='legacy' THEN
  UPDATE vault.credentials SET password_hash=new_hash WHERE user_id=account AND password_hash=old_hash;
 ELSIF method='password' THEN
  UPDATE vault.user_identities SET password_hash=new_hash WHERE user_id=account AND provider='password' AND password_hash=old_hash;
 ELSE RETURN false;
 END IF;
 IF NOT FOUND THEN RETURN false; END IF;
 UPDATE vault.users SET auth_version=auth_version+1 WHERE id=account;
 UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE user_id=account AND revoked_at IS NULL;
 DELETE FROM vault.password_reset_challenges WHERE user_id=account;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION vault.issue_password_reset(BYTEA,BYTEA,TEXT),vault.discard_password_reset(BYTEA,BYTEA),vault.claim_password_reset_attempt(BYTEA),vault.complete_password_reset(BYTEA,BYTEA,TEXT),vault.change_account_password(UUID,BYTEA,TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION vault.issue_password_reset(BYTEA,BYTEA,TEXT),vault.discard_password_reset(BYTEA,BYTEA),vault.claim_password_reset_attempt(BYTEA),vault.complete_password_reset(BYTEA,BYTEA,TEXT),vault.change_account_password(UUID,BYTEA,TEXT,TEXT,TEXT) TO vault_runtime;
COMMIT;
