BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
CREATE TABLE vault.file_shares (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 file_id UUID NOT NULL REFERENCES vault.files(id) ON DELETE CASCADE,
 owner_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE CASCADE,
 token_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),
 recipient_id UUID REFERENCES vault.users(id) ON DELETE CASCADE,
 permission TEXT NOT NULL CHECK(permission IN ('DOWNLOAD','PREVIEW_AND_DOWNLOAD')),
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 CHECK(recipient_id IS NULL OR recipient_id<>owner_id),
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '30 days')
);
CREATE INDEX file_shares_file_idx ON vault.file_shares(file_id,created_at,id);
CREATE INDEX file_shares_owner_idx ON vault.file_shares(owner_id);
CREATE INDEX file_shares_recipient_idx ON vault.file_shares(recipient_id);
CREATE TABLE vault.shared_access (
 token_hash BYTEA PRIMARY KEY CHECK(octet_length(token_hash)=32),
 share_id UUID NOT NULL REFERENCES vault.file_shares(id) ON DELETE CASCADE,
 session_hash BYTEA NOT NULL REFERENCES vault.sessions(token_hash) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN ('DOWNLOAD','PREVIEW')),
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 download_counted BOOLEAN NOT NULL DEFAULT false,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '1 minute')
);
CREATE INDEX shared_access_session_idx ON vault.shared_access(session_hash,expires_at);
CREATE INDEX shared_access_share_idx ON vault.shared_access(share_id);
CREATE INDEX shared_access_expiry_idx ON vault.shared_access(expires_at);
CREATE TABLE vault.shared_request_windows (
 session_hash BYTEA PRIMARY KEY REFERENCES vault.sessions(token_hash) ON DELETE CASCADE,
 accepted_at TIMESTAMPTZ[] NOT NULL DEFAULT '{}',
 CHECK(cardinality(accepted_at)<=2)
);
CREATE TABLE vault.file_download_counts (
 file_id UUID PRIMARY KEY REFERENCES vault.files(id) ON DELETE CASCADE,
 shared_downloads BIGINT NOT NULL DEFAULT 0 CHECK(shared_downloads>=0)
);
GRANT SELECT,INSERT,DELETE ON vault.file_shares,vault.shared_access TO vault_runtime;
GRANT UPDATE(expires_at) ON vault.file_shares TO vault_runtime;
GRANT UPDATE(download_counted) ON vault.shared_access TO vault_runtime;
GRANT SELECT,INSERT ON vault.shared_request_windows,vault.file_download_counts TO vault_runtime;
GRANT UPDATE(accepted_at) ON vault.shared_request_windows TO vault_runtime;
GRANT UPDATE(shared_downloads) ON vault.file_download_counts TO vault_runtime;
GRANT SELECT,DELETE ON vault.shared_access TO vault_gc;
COMMENT ON TABLE vault.file_shares IS 'Read-only bearer links; optional recipient restriction. Raw tokens are returned once and never stored.';
COMMENT ON TABLE vault.file_download_counts IS 'Admitted shared DOWNLOAD GET opens, once per short-lived grant; not completed transfers. HEAD and PREVIEW do not count.';
COMMIT;
