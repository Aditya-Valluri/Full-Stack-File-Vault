# GraphQL session bootstrap

## WHAT

`Mutation.beginSession: SessionBootstrap!` returns only `csrfToken: String!`.
An absent, malformed, expired or revoked credential results in a new anonymous
session with a fixed ten-minute expiry. A valid anonymous or authenticated session
is reused without renewal, identity changes or a replacement cookie.

## WHY & TRADE-OFFS

This is an application ADR. A narrow bootstrap exception lets a browser recover its
CSRF token after reload while keeping the session credential HttpOnly. An unconditional
header exception would allow unrelated operations without their synchronizer token.
Instead, a bounded JSON body is parsed and schema-validated; only a selected mutation
with exactly one expanded beginSession root is allowed. Aliases and fragments are
supported, but repeated/mixed roots and directives on root selections are rejected.
Origin and Fetch Metadata checks still apply. Allocation occurs inside the resolver,
after gqlgen's own validation, through a capability installed by the boundary.

A singleton PostgreSQL row serializes allocations across replicas. Compared with
in-memory counters it survives process changes and shares a budget; compared with
Redis it avoids another service but adds a database lock and a global bottleneck.
The default is 60 allocations per database minute, configured identically on all
replicas with BOOTSTRAP_CREATIONS_PER_MINUTE (1..600). Insert and budget increment
commit together. Clock rollback does not reset the budget. Valid-session reuse
does not consume it. Fixed windows permit bursts across minute boundaries and a
global budget can be exhausted by one actor. This guard is separate from the required
strict per-user two-calls-per-second limiter, which is not implemented here.

## THE HOW

- Migration 000006 adds the budget row and narrow runtime privileges.
- internal/auth/bootstrap.go owns reuse and transactional allocation.
- internal/graph/bootstrap.go validates operations and composes the browser handler.
- cmd/server wires the configured budget into that handler.
- The resolver exposes CSRF material only; cookie issuance follows a committed insert.

After starting the API with scripts/start-api.ps1, bootstrap from PowerShell:

```powershell
$vaultBrowser = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$vaultBody = @{ query = 'mutation { beginSession { csrfToken } }' } | ConvertTo-Json
$vaultReply = Invoke-RestMethod http://127.0.0.1:8080/graphql -Method Post -ContentType application/json -WebSession $vaultBrowser -Headers @{ Origin = 'http://127.0.0.1:8080'; 'X-Vault-CSRF-Bootstrap' = '1' } -Body $vaultBody
$vaultCSRF = $vaultReply.data.beginSession.csrfToken
```

Keep CSRF material in memory. Subsequent ordinary requests send X-CSRF-Token with
the cookie; bootstrap recovery sends X-Vault-CSRF-Bootstrap: 1 instead. Production
requires HTTPS and uses __Host-vault_session with Secure, HttpOnly, SameSite=Lax,
Path=/ and no Domain. Explicit loopback development uses vault_session_dev.
Responses are no-store. GraphQL allocation failures return a safe RATE_LIMITED
error at HTTP 200; storage errors remain generic. Invalid bootstrap documents are
rejected before execution with HTTP 400. Missing bootstrap capability returns
FORBIDDEN. Never retry creation in an unbounded loop.

## VALIDATION

Operation-boundary tests cover aliases, fragments, operation selection, directives,
duplicate/mixed roots, malformed/oversized bodies and multipart rejection.
PostgreSQL/TLS integration tests cover cookie flags, anonymous and authenticated
reuse, revoked-cookie replacement, rejection without allocations, replica concurrency,
safe rate-limit responses and transactional rollback after an injected insert failure.
Unit tests, go vet and build passed. The isolated PostgreSQL integration suite passed
in 11.422 seconds. Migration 000006 was applied successfully to the local database.

Browser acceptance tests remain outstanding. Expired-row cleanup and ingress abuse
controls are still needed before public deployment; the budget bounds allocation
rate, not total retained rows. This step does not expose login, me or logout.

## ACTIONABLE MOMENTUM

Next bounded step: GraphQL login with anonymous-session rotation and login throttling,
or review the bootstrap contract before adding authentication operations.
