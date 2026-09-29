BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
CREATE TABLE vault.share_activity (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 file_id UUID NOT NULL REFERENCES vault.files(id) ON DELETE CASCADE,
 share_id UUID NOT NULL,
 recipient_id UUID REFERENCES vault.users(id) ON DELETE SET NULL,
 kind TEXT NOT NULL CHECK(kind IN ('OPENED','DOWNLOAD_STARTED')),
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX share_activity_file_idx ON vault.share_activity(file_id,id DESC);
GRANT SELECT,INSERT,DELETE ON vault.share_activity TO vault_runtime;
GRANT USAGE ON SEQUENCE vault.share_activity_id_seq TO vault_runtime;
COMMIT;
