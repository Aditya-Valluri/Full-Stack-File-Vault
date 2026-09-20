BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
-- Immutable, owner-scoped receipts commit atomically with logical files and quota.
-- Retain receipts so an old retry can never become a new charged upload.
CREATE TABLE vault.upload_receipts (
 owner_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE CASCADE,
 request_key UUID NOT NULL,
 fingerprint BYTEA NOT NULL CHECK(octet_length(fingerprint)=32),
 result JSONB NOT NULL CHECK(jsonb_typeof(result)='array' AND jsonb_array_length(result) BETWEEN 1 AND 100),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,request_key)
);
GRANT SELECT,INSERT ON vault.upload_receipts TO vault_runtime;
COMMIT;
