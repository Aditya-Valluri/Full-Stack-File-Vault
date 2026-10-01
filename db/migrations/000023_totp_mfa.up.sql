BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
CREATE TABLE vault.user_mfa (
 user_id UUID PRIMARY KEY REFERENCES vault.users(id) ON DELETE CASCADE,
 encrypted_secret BYTEA NOT NULL CHECK(octet_length(encrypted_secret)=49),
 enabled BOOLEAN NOT NULL DEFAULT false,
 setup_expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '10 minutes',
 last_step BIGINT NOT NULL DEFAULT -1 CHECK(last_step>=-1)
);
CREATE TABLE vault.mfa_recovery_codes (
 user_id UUID NOT NULL REFERENCES vault.user_mfa(user_id) ON DELETE CASCADE,
 code_hash BYTEA NOT NULL CHECK(octet_length(code_hash)=32),
 PRIMARY KEY(user_id,code_hash)
);
GRANT SELECT,INSERT,UPDATE,DELETE ON vault.user_mfa TO vault_runtime;
GRANT SELECT,INSERT,DELETE ON vault.mfa_recovery_codes TO vault_runtime;
COMMENT ON TABLE vault.user_mfa IS 'AES-GCM encrypted 20-byte TOTP seed; server-side key, nonce and version; never a plaintext seed. One expiring pending enrollment per account.';
COMMENT ON TABLE vault.mfa_recovery_codes IS 'SHA-256 hashes of independently generated high-entropy one-use recovery codes; never raw codes.';
COMMIT;
