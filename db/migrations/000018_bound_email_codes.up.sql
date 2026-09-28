BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
ALTER TABLE vault.email_challenges
 ADD COLUMN binding_hash BYTEA UNIQUE CHECK(binding_hash IS NULL OR octet_length(binding_hash)=32),
 ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5);
-- No runtime UPDATE privilege. The only transition increments an unexpired
-- browser-bound challenge's counter, never resets or extends it.
CREATE FUNCTION vault.claim_email_attempt(browser_hash BYTEA)
RETURNS BYTEA LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,vault AS $$
 UPDATE vault.email_challenges SET attempts=attempts+1
 WHERE binding_hash=browser_hash AND attempts<5 AND expires_at>clock_timestamp()
 RETURNING token_hash;
$$;
REVOKE ALL ON FUNCTION vault.claim_email_attempt(BYTEA) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION vault.claim_email_attempt(BYTEA) TO vault_runtime;
COMMIT;
