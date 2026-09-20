BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
CREATE TABLE vault.login_budget (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    window_start TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL CHECK (attempts >= 0)
);
INSERT INTO vault.login_budget VALUES (1,date_trunc('minute',clock_timestamp()),0);
CREATE TABLE vault.login_attempts (
    scope TEXT NOT NULL CHECK (scope IN ('peer','identifier')),
    key_hash BYTEA NOT NULL CHECK (octet_length(key_hash)=32),
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL CHECK (attempts>0),
    PRIMARY KEY(scope,key_hash)
);
CREATE INDEX login_attempts_expiry_idx ON vault.login_attempts(expires_at);
GRANT SELECT ON vault.login_budget TO vault_runtime;
GRANT UPDATE(window_start,attempts) ON vault.login_budget TO vault_runtime;
GRANT SELECT,INSERT,DELETE ON vault.login_attempts TO vault_runtime;
GRANT UPDATE(expires_at,attempts) ON vault.login_attempts TO vault_runtime;
COMMIT;
