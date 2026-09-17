# Authenticated session lifecycle

Follow-up: migration 5 and [pre-login rotation](prelogin.md) extend this original
authenticated-only implementation with anonymous sessions and user-version locking.
The original scope and race limitations below describe the migration-4 milestone;
the follow-up documents current creation/bulk-revocation coordination.

## Scope and decision

ADR: shared PostgreSQL opaque sessions, not process-local sessions or JWTs. Migration
4 adds authenticated session records. `internal/auth/sessions.go` provides creation,
lookup/activity renewal, individual revocation and revocation of existing user sessions.
The store is not yet wired into HTTP. The current GraphQL API remains unchanged.

No cookie, login, anonymous bootstrap or CSRF enforcement is implemented here. A random
CSRF token is stored as future synchronizer-token material; its existence alone offers
no CSRF protection. Anonymous sessions need a later explicit schema/lifecycle addition.
Create is an internal trusted operation whose caller must first verify credentials.

## Storage and time

Authentication tokens contain 32 crypto/rand bytes, base64url encoded. PostgreSQL stores
only SHA-256 of those random bytes, not the bearer token. Creation returns the raw
token once. Session metadata and CSRF values must not be logged or directly serialized
as a user API model. CSRF tokens are independent random values, stored server-side.

The constructor requires a pool and whole-second idle/absolute lifetimes with
1 second <= idle <= absolute <= 30 days. Suggested deployment settings remain 30 minutes
idle / 24 hours absolute. The API config does not expose these values yet because it
does not instantiate the store. Database clock_timestamp determines lifecycle times.

Lookup uses a short READ COMMITTED transaction: lock the session row; then conditionally
renew only if not revoked, not expired and the user is enabled. Time is sampled after
lock acquisition. Idle renewal is capped at absolute expiry. Each operation has a
five-second deadline, bounded further by the caller's context.

Both renewal and revocation update the same row. If revocation commits first, renewal
fails; if renewal commits first, subsequent revocation still marks the row. A lookup
that completed before revocation may return a usable snapshot: it cannot promise to
cancel every in-flight request. Sensitive writes must recheck eligibility in their
own transaction when those operations are implemented.

User role is read at lookup time, never trusted from token content. Disabled users
cannot create or look up sessions. Disabling alone does not permanently revoke an
old session after re-enablement; future account-disable handling must also revoke
sessions transactionally. RevokeUser affects existing rows, not concurrent creation:
login/account-change flows must establish shared user-level coordination. Do not
compose Revoke + Create as a substitute for atomic login rotation.

## Privileges and failures

Runtime grants: SELECT, INSERT and UPDATE of activity/idle-expiry/revocation columns.
No UPDATE of token_hash, user_id, CSRF value or absolute expiry; no DELETE privilege.
This is an application trust boundary, not protection against a compromised API role,
which can insert sessions. Administrative cleanup is deferred; expiration checks do
not depend on cleanup running. Expiry/user indexes support future cleanup/revocation.

Unknown, malformed, expired, disabled-user and revoked sessions return ErrInvalidSession.
Store errors return ErrSessionStore without connection details. There is no positive
cache or fallback authentication. A later HTTP layer must map these errors deliberately.

## Validation commands

From apps/api with Go and Docker available:

```powershell
go test -count=1 ./...
go test -tags integration -run TestProvisionCommandIntegration -count=1 -v ./cmd/provision-user
go vet -tags integration ./...
go build ./...
```

The existing isolated provisioning fixture now applies migrations 1-4 and runs a
session-lifecycle subtest through two independent runtime-role pools. It observes a
lookup blocked on an uncommitted revocation, commits it, and asserts rejection.
It also checks expiry, disabled accounts, bulk revocation, privileges and closed-pool
failure. Expiry is induced through administrator SQL rather than wall-clock sleeps.

Apply migration 4 to development only with the normal migration command:

```powershell
docker compose run --rm migrate
```

Down migration destroys sessions and logs users out; restrict rollback validation to
disposable databases. No production deployment or browser-security validation is
implied by these store-level tests.

## Results recorded 2026-09-16

- Unit tests, vet (including integration sources) and build passed.
- Isolated provisioning/session integration test passed in 19.59 seconds; session
  subtest passed in 0.35 seconds. Test container and anonymous volume were removed.
- Migration 4 applied to local development PostgreSQL: version 4, dirty=false.
- No browser login, session rotation, CSRF or production deployment test was run.
