BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='30s';
CREATE TABLE vault.admin_audit (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE RESTRICT,
 target_user_id UUID NOT NULL REFERENCES vault.users(id) ON DELETE RESTRICT,
 action TEXT NOT NULL CHECK(action IN ('USER_QUOTA_CHANGED','USER_DISABLED','USER_ENABLED')),
 occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 previous_quota BIGINT NOT NULL CHECK(previous_quota>=0),
 new_quota BIGINT NOT NULL CHECK(new_quota>=0),
 previous_disabled_at TIMESTAMPTZ,
 new_disabled_at TIMESTAMPTZ,
 revoked_sessions BIGINT NOT NULL DEFAULT 0 CHECK(revoked_sessions>=0),
 revoked_shares BIGINT NOT NULL DEFAULT 0 CHECK(revoked_shares>=0)
);
CREATE INDEX admin_audit_target_idx ON vault.admin_audit(target_user_id,id DESC);
CREATE INDEX admin_audit_actor_idx ON vault.admin_audit(actor_id);
GRANT SELECT,INSERT ON vault.admin_audit TO vault_runtime;
GRANT USAGE ON SEQUENCE vault.admin_audit_id_seq TO vault_runtime;
GRANT UPDATE(quota_bytes,disabled_at) ON vault.users TO vault_runtime;
COMMENT ON TABLE vault.admin_audit IS 'Append-only to the runtime role; successful administrative mutations commit with their audit record. No credentials or content.';
COMMIT;
