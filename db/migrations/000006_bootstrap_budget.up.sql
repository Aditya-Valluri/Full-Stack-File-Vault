BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';
CREATE TABLE vault.bootstrap_budget (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    window_start TIMESTAMPTZ NOT NULL,
    creations INTEGER NOT NULL CHECK (creations >= 0)
);
INSERT INTO vault.bootstrap_budget VALUES (1, date_trunc('minute',clock_timestamp()), 0);
GRANT SELECT ON vault.bootstrap_budget TO vault_runtime;
GRANT UPDATE(window_start,creations) ON vault.bootstrap_budget TO vault_runtime;
COMMENT ON TABLE vault.bootstrap_budget IS 'Global fixed-window anonymous-session allocation guard; distinct from per-user API rate limiting.';
COMMIT;
