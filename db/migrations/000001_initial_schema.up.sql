BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE SCHEMA vault;

REVOKE ALL ON SCHEMA vault FROM PUBLIC;


-- ============================================================
-- USERS
--
-- Authentication-specific fields are intentionally deferred.
-- This table represents the application's stable internal user.
-- ============================================================

CREATE TABLE vault.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The assignment specifies a default 10 MB quota.
    -- 10 MB is a DEFAULT, not a maximum.
    quota_bytes BIGINT NOT NULL DEFAULT 10000000,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT users_quota_nonnegative
        CHECK (quota_bytes >= 0)
);


-- ============================================================
-- BLOBS
--
-- Represents unique physical file content.
-- Multiple logical files may reference the same blob.
-- ============================================================

CREATE TABLE vault.blobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- SHA-256 = exactly 32 raw bytes.
    sha256 BYTEA NOT NULL,

    size_bytes BIGINT NOT NULL,

    -- Opaque internal storage locator.
    -- Never derived directly from a user-provided filename.
    storage_key TEXT NOT NULL,

    detected_mime TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT blobs_sha256_unique
        UNIQUE (sha256),

    CONSTRAINT blobs_sha256_length
        CHECK (octet_length(sha256) = 32),

    CONSTRAINT blobs_size_nonnegative
        CHECK (size_bytes >= 0),

    CONSTRAINT blobs_storage_key_unique
        UNIQUE (storage_key),

    CONSTRAINT blobs_storage_key_valid
        CHECK (
            char_length(btrim(storage_key))
            BETWEEN 1 AND 1024
        )
);


-- ============================================================
-- FILES
--
-- Represents a user's logical file.
--
-- Ownership and physical content are deliberately separated.
-- ============================================================

CREATE TABLE vault.files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    owner_id UUID NOT NULL,
    blob_id UUID NOT NULL,

    -- User-facing display filename only.
    -- Must never be used directly as a physical storage path.
    original_name TEXT NOT NULL,

    -- Client claim retained for diagnostics / validation history.
    declared_mime TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT files_owner_fk
        FOREIGN KEY (owner_id)
        REFERENCES vault.users (id)
        ON DELETE RESTRICT,

    CONSTRAINT files_blob_fk
        FOREIGN KEY (blob_id)
        REFERENCES vault.blobs (id)
        ON DELETE RESTRICT,

    CONSTRAINT files_original_name_valid
        CHECK (
            char_length(btrim(original_name))
            BETWEEN 1 AND 255
        )
);


-- ============================================================
-- INDEXES
-- ============================================================

-- User file listing / chronological pagination.
CREATE INDEX files_owner_created_id_idx
    ON vault.files (
        owner_id,
        created_at DESC,
        id DESC
    );

-- Blob reference lookup.
-- Important for safe deletion / garbage collection.
CREATE INDEX files_blob_id_idx
    ON vault.files (blob_id);


-- ============================================================
-- DOCUMENTATION COMMENTS
-- ============================================================

COMMENT ON TABLE vault.users IS
    'Internal application users. Authentication identity fields are added separately once the authentication architecture is finalized.';

COMMENT ON COLUMN vault.users.quota_bytes IS
    'Logical storage quota in bytes. Default is 10,000,000 bytes but higher configured quotas are permitted.';

COMMENT ON TABLE vault.blobs IS
    'Unique physical file content identified by SHA-256. Knowledge of a blob hash never grants authorization.';

COMMENT ON COLUMN vault.blobs.storage_key IS
    'Opaque storage locator controlled by the application, never by the uploaded filename.';

COMMENT ON TABLE vault.files IS
    'User-owned logical file metadata. Multiple files may reference the same deduplicated blob.';


COMMIT;
