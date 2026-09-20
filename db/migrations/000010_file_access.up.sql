BEGIN;
SET LOCAL lock_timeout='5s';
CREATE TABLE vault.file_access (
 token_hash BYTEA PRIMARY KEY CHECK(octet_length(token_hash)=32),
 owner_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE CASCADE,
 session_hash BYTEA NOT NULL REFERENCES vault.sessions(token_hash) ON DELETE CASCADE,
 file_id UUID NOT NULL REFERENCES vault.files(id) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN ('DOWNLOAD','PREVIEW')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 CHECK(expires_at > created_at AND expires_at <= created_at + interval '1 minute')
);
CREATE INDEX file_access_owner_expiry_idx ON vault.file_access(owner_id,expires_at);
CREATE INDEX file_access_expiry_idx ON vault.file_access(expires_at);
CREATE INDEX file_access_file_idx ON vault.file_access(file_id);
CREATE INDEX file_access_session_idx ON vault.file_access(session_hash);
CREATE INDEX blobs_gc_due_idx ON vault.blobs(gc_after,id) WHERE state='GC_PENDING';
GRANT SELECT,INSERT,DELETE ON vault.file_access TO vault_runtime;
GRANT DELETE ON vault.files TO vault_runtime;
COMMENT ON TABLE vault.file_access IS 'Short-lived hashed capability bound to the issuing session and owned logical file. Never a public share.';
COMMIT;
