BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
-- Only hashes of random 256-bit verification codes are persisted.
CREATE TABLE vault.email_challenges (
 token_hash BYTEA PRIMARY KEY CHECK(octet_length(token_hash)=32),
 email_address TEXT NOT NULL CHECK(octet_length(email_address) BETWEEN 3 AND 254),
 email_normalized TEXT COLLATE "C" NOT NULL CHECK(email_normalized=lower(email_normalized)),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP+interval '15 minutes')
);
CREATE INDEX email_challenges_expiry ON vault.email_challenges(expires_at);
GRANT SELECT,INSERT,DELETE ON vault.email_challenges TO vault_runtime;

-- Narrow creation capability: callers cannot set roles, quotas or existing identities.
CREATE FUNCTION vault.complete_email_registration(code_hash BYTEA, encoded_password TEXT)
RETURNS UUID LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
DECLARE challenge vault.email_challenges%ROWTYPE; account_id UUID;
BEGIN
 IF encoded_password IS NULL OR encoded_password !~ '^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$' THEN
  RETURN NULL;
 END IF;
 DELETE FROM vault.email_challenges WHERE token_hash=code_hash AND expires_at>clock_timestamp()
 RETURNING * INTO challenge;
 IF NOT FOUND THEN RETURN NULL; END IF;
 INSERT INTO vault.users(email_address,email_normalized,email_verified_at)
 VALUES(challenge.email_address,challenge.email_normalized,clock_timestamp())
 ON CONFLICT(email_normalized) WHERE email_normalized IS NOT NULL DO NOTHING
 RETURNING id INTO account_id;
 IF account_id IS NULL THEN RETURN NULL; END IF;
 INSERT INTO vault.user_identities(user_id,provider,provider_subject,password_hash)
 VALUES(account_id,'password',challenge.email_normalized,encoded_password);
 RETURN account_id;
END $$;
REVOKE ALL ON FUNCTION vault.complete_email_registration(BYTEA,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION vault.complete_email_registration(BYTEA,TEXT) TO vault_runtime;
COMMIT;
