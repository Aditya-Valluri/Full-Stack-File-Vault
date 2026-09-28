BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Existing username accounts remain unchanged: no inferred emails or identity backfill.
-- One verified email per user is sufficient for the current product.
ALTER TABLE vault.users
 ADD COLUMN email_address TEXT,
 ADD COLUMN email_normalized TEXT COLLATE "C",
 ADD COLUMN email_verified_at TIMESTAMPTZ,
 ADD CONSTRAINT users_verified_email_complete CHECK (
  (email_address IS NULL AND email_normalized IS NULL AND email_verified_at IS NULL)
  OR (email_address IS NOT NULL AND email_normalized IS NOT NULL AND email_verified_at IS NOT NULL
      AND octet_length(email_address) BETWEEN 3 AND 254
      AND octet_length(email_normalized) BETWEEN 3 AND 254
      AND email_normalized = lower(email_normalized) AND email_normalized = btrim(email_normalized))
 );
CREATE UNIQUE INDEX users_verified_email_unique ON vault.users(email_normalized)
 WHERE email_normalized IS NOT NULL;

CREATE TABLE vault.user_identities (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE RESTRICT,
 provider TEXT NOT NULL CHECK (provider IN ('password', 'google')),
 provider_subject TEXT COLLATE "C" NOT NULL CHECK (octet_length(provider_subject) BETWEEN 1 AND 255),
 password_hash TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(provider, provider_subject),
 UNIQUE(user_id, provider),
 CONSTRAINT identity_password_kind CHECK (
  (provider = 'password' AND password_hash IS NOT NULL AND octet_length(password_hash) BETWEEN 80 AND 512)
  OR (provider = 'google' AND password_hash IS NULL)
 )
);
-- No runtime write grants: later slices expose narrowly scoped verified operations.
GRANT SELECT ON vault.user_identities TO vault_runtime;
COMMENT ON TABLE vault.user_identities IS 'Additive identity methods; legacy username hashes remain in vault.credentials. Never link users by matching provider emails.';
COMMENT ON COLUMN vault.users.email_normalized IS 'Unique verified account email; registration verifies mailbox ownership before assignment. Existing users remain NULL.';
COMMIT;
