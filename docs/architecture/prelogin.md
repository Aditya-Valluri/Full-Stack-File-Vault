# Anonymous sessions and atomic login rotation

## Implemented scope

Migration 5 permits nullable session user_id for anonymous sessions, enforces a
maximum ten-minute anonymous lifetime, and adds users.auth_version. No HTTP route,
cookie or GraphQL schema change is included. These remain internal service methods.

CreateAnonymous generates independent random session and CSRF tokens and stores
only the authentication-token digest. LookupAnonymous does not extend lifetime.
Authenticated LookupAndTouch rejects anonymous sessions. Bootstrap creation must
eventually be gated by Origin, a non-simple header, operation checks and abuse limits.

Login requires a valid anonymous session and matching CSRF token. It normalizes the
login identifier, reads the enabled user's credential/security version and verifies
Argon2id before taking database locks. Missing/disabled users use a per-store dummy
hash to reduce obvious timing distinctions; this is not a claim of identical timing.
Password hashing is bounded to two concurrent operations per SessionStore instance;
create one shared store per API process. This is not a distributed rate limiter.
The operation has a ten-second context deadline; Argon2 itself is not interruptible.

## Transaction and lock order

1. Lock the user row at READ COMMITTED, checking enabled status and auth_version.
2. Re-read the credential hash and reject a changed credential.
3. Lock the anonymous session row.
4. Recheck anonymous type, expiry, revocation and CSRF binding, then revoke it.
5. Insert a new authenticated session with fresh authentication and CSRF tokens.
6. Commit before returning any token to the caller.

The lock order is user then session. Failed insertion or commit never returns a
usable new token. An insertion failure rolls back anonymous consumption. An uncertain
network result during commit can leave an unreachable authenticated session until
expiry; prefer that bounded leak to returning a token whose transaction is uncertain.
Concurrent attempts can verify credentials simultaneously, but only one can consume
an anonymous session. A successful login does not modify the anonymous row's identity.

RevokeUser now locks/increments auth_version and revokes existing sessions in one
transaction. A pending login verified against an older version is rejected. Trusted
Create also locks the user before publishing sessions. New logins initiated after
revocation can succeed; revocation is not an account ban. UPDATE of disabled_at
naturally conflicts with the user row lock, and login rechecks disabled state.

Future password changes, role changes and account disabling must lock the user first,
increment auth_version and revoke sessions in the same transaction. Direct credential
updates outside this protocol are not a supported account-management flow. There is
no password-change/admin endpoint yet. A login committed before disabling may return
to its caller afterward, but subsequent authenticated lookup rejects disabled users.

## Failures and browser boundary

Wrong credentials, missing/expired/consumed anonymous sessions and wrong CSRF token
return ErrLoginRejected. Database/hash-format failures return ErrSessionStore without
raw database messages. Failed login leaves anonymous state available until expiry.

This is only the session-bound token check, not complete browser CSRF protection.
Origin/Fetch Metadata policy, restrictive CORS, secure cookies, operation classification,
typed GraphQL errors and login/bootstrap throttling must precede a public login API.
Tokens must not appear in logs or GraphQL models. HTTP may set the authentication
cookie only after Login returns successfully. The internal CSRF value may be returned
through the specifically designed bootstrap/login response later.

The down migration intentionally fails while any anonymous rows exist. It never
silently deletes sessions merely to restore a NOT NULL constraint. Rollbacks need
an explicit operational plan and should be tested in disposable environments.

## Verification

Run from apps/api with Go/Docker on PATH:

```powershell
go test -count=1 ./...
go test -tags integration -count=1 -v ./cmd/provision-user
go vet -tags integration ./...
go build ./...
```

The isolated PostgreSQL fixture applies migrations 1-5. Tests cover wrong/missing
credentials, wrong CSRF, preserved state after rejection, expiry, replay, fresh tokens,
insert-failure rollback, two competing logins and user-version/disable lock races.
Existing provisioning and authenticated-session tests run alongside these assertions.

Recorded result: unit tests, vet and build passed. The first integration attempt
stopped at Docker-engine availability; after starting Docker Desktop, the suite passed
in 8.56 seconds (pre-login subtest 0.48 seconds), including test-container cleanup.
No browser-cookie or GraphQL authentication test is claimed by this result.

Migration 5 also applied to the local development database; schema_migrations reports
version 5 and dirty=false. No account/session token was printed during validation.
