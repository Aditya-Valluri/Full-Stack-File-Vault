BEGIN;
SET LOCAL lock_timeout = '5s';
CREATE TABLE vault.user_request_windows (
 user_id UUID PRIMARY KEY REFERENCES vault.users(id) ON DELETE CASCADE,
 accepted_at TIMESTAMPTZ[] NOT NULL DEFAULT '{}',
 CONSTRAINT bounded_request_window CHECK (cardinality(accepted_at) <= 100)
);
GRANT SELECT, INSERT ON vault.user_request_windows TO vault_runtime;
GRANT UPDATE(accepted_at) ON vault.user_request_windows TO vault_runtime;
COMMENT ON TABLE vault.user_request_windows IS 'At most one bounded rolling-second admission window per user, shared by API replicas.';
COMMIT;
