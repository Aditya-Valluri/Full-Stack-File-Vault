# ADR 0004: Browser authentication, sessions and CSRF

Status: Identity source and operator provisioning accepted by the user on 2026-09-15.
Authenticated PostgreSQL session lifecycle subsequently authorized on 2026-09-16.
Browser cookie helpers and centralized Origin/session-bound CSRF checks have now been
authorized and implemented. GraphQL bootstrap/login and the authenticated me query
are implemented, along with authenticated session logout.

Follow-up: anonymous-session primitives and atomic login rotation were subsequently
authorized and implemented; see ../architecture/prelogin.md. The subsequent browser
boundary is documented in ../architecture/browser-security.md. The operation-aware
bootstrap exception is implemented. Login/me follow-ups are documented in
../architecture/graphql-login.md and ../architecture/current-user.md.

## Context and classification

SPEC (per the supplied assignment summary, not independently verified against the
PDF): secure file ownership and backend-enforced access control.

Accepted ADR constraints: GraphQL-only application operations, browser credentials
in Secure HttpOnly cookies, one public origin, explicit CSRF defenses, PostgreSQL,
and centralized authorization. This proposal preserves ADR 0003.

At proposal time, code exposed only public serviceInfo. Users had IDs and quotas but no
credentials, roles or sessions. The runtime role has SELECT only. The GraphQL
presenter currently treats all resolver errors as internal; typed safe auth errors
will need deliberate introduction. Do not silently edit existing migrations.

## Alternatives and recommendation

| Choice | Benefits | Costs |
|---|---|---|
| First-party credentials | Self-contained reviewer setup; no external tenant | Password handling, throttling and account lifecycle become our responsibility |
| OIDC provider | Delegated identity, recovery and potentially MFA | Provider setup and protocol integration; provider availability becomes a dependency |
| PostgreSQL opaque sessions | Immediate server-side revocation, shared multi-replica state | Database lookup per request; expiry cleanup |
| Signed JWT sessions | Local verification | Revocation/role changes require additional state or stale-access windows |

Recommend first-party credentials and PostgreSQL-backed opaque sessions for the
initial submission. OIDC is a viable alternative, not an inferior architecture.
No Redis is required for session storage. No long-lived bearer tokens in browser
storage. These recommendations are ADRs, not assignment requirements.

Accepted follow-up: first-party identity with operator-provisioned reviewer accounts, not public
self-registration. Registration, email verification and password recovery are
separate product scope; do not claim a complete public identity service without them.

## Session design

- Generate 32 random bytes using crypto/rand, encode as base64url, and place the raw
  token only in the session cookie. Store a SHA-256 digest as the indexed lookup key.
  Hashing a random high-entropy session token is different from hashing a password.
- Proposed session metadata: token digest, user ID, creation time, last activity,
  idle expiry, absolute expiry, revocation time, and CSRF token. Store the independent
  random CSRF token server-side for re-fetch after page reload; never return the
  authentication token in GraphQL JSON. Do not describe CSRF tokens as password hashes.
- Proposed configurable defaults: authenticated absolute lifetime 24 hours, idle
  lifetime 30 minutes, anonymous pre-auth session lifetime 10 minutes. These values
  are policy assumptions, not requirements. Cookie expiry never overrides DB expiry.
- Validate session state and current user status/role on every authenticated request.
  Use a conditional database update/read for activity renewal so logout cannot be
  undone by an in-flight request. Touch writes may be optimized later with explicitly
  bounded idle-expiry semantics; correctness precedes reducing writes.
- Successful login atomically consumes the pre-auth session, creates a fresh session
  and CSRF token, and sends Set-Cookie only after commit. Do not upgrade an anonymous
  token in place. Simultaneous login attempts must not reuse consumed pre-auth state.
- Logout revokes the current session in PostgreSQL and expires the cookie. Password
  change, account disable and security-sensitive role changes revoke all user sessions.
- Revocation rejects subsequent authorization checks. Already authorized reads may
  finish; sensitive writes must recheck account/session eligibility in their transaction.
  Do not promise cancellation of every in-flight operation at logout.
- Database failure fails authentication closed; never use a stale positive cache.
  Cleanup removes expired/revoked rows but expiry enforcement does not depend on it.

## Cookie and deployment contract

Production cookie: `__Host-vault_session`, Secure, HttpOnly, SameSite=Lax, Path=/,
no Domain. Clear using matching scope. Lax is defense-in-depth, not the CSRF policy.
HTTPS terminates at a trusted ingress; trust forwarded headers only from configured
proxies. Determine allowed Origin from explicit configuration, not a client Host header.

Prefer local HTTPS. An explicitly enabled local HTTP mode may use a distinct unprefixed
development cookie with Secure=false, restricted to loopback deployment. Production
startup must reject that mode. No automatic fallback based on an incoming request.
Use a Vite same-origin proxy when the frontend arrives rather than broadening CORS.

## CSRF bootstrap and enforcement

Use a random synchronizer token bound to the current anonymous/authenticated session.
Return it through GraphQL and keep it in client memory. Send `X-CSRF-Token` on requests;
compare it in constant time. The HttpOnly session token remains inaccessible to JS.

1. Apply the configured exact Origin allowlist to browser application requests.
   Reject absent, null, malformed or foreign Origin for the proposed browser API.
   CLI/integration clients must send the configured Origin; this is not authentication.
2. Reject explicitly cross-site Fetch Metadata. Missing Fetch Metadata is compatible
   with non-browser clients and must not bypass Origin/token checks. Same-site is
   not equivalent to same-origin.
3. Deny cross-origin credentialed access; no wildcard CORS or reflected origins.
4. An initial `beginSession` mutation is a narrow bootstrap exception: JSON POST,
   valid Origin, fixed non-simple `X-Vault-CSRF-Bootstrap: 1` header, and exactly one
   selected top-level field after fragment/alias expansion. No other operation may
   execute through this exception. No multipart bootstrap or batch array.
5. Bootstrap creates an anonymous session only if no valid session exists. With an
   existing valid session, return that session's CSRF token without replacing its
   identity. This supports reload/multiple tabs without rotating tokens unexpectedly.
6. Login must present the anonymous session cookie and its CSRF token. Rotate both
   after success. Logout and all future authenticated operations require token checks.
7. serviceInfo may remain public and token-free, but mixing it with protected fields
   never bypasses protection. Authentication and operation checks must complete before
   resolver execution. Centralize operation classification using the selected GraphQL
   operation/AST, never query-string matching or client operation names.
8. Future multipart requests must pass the same Origin and session-bound token gate
   before expensive parsing/staging. Their exception policy is not the bootstrap policy.

The bootstrap's custom header is one layer alongside exact Origin and restrictive
CORS, not a claim that custom headers alone prevent every CSRF attack. Rate-limit
bootstrap creation to prevent anonymous-session table exhaustion.

## Credentials and abuse prevention

If first-party credentials are approved, use a maintained Argon2id implementation,
random salt, encoded algorithm parameters and constant-time verification. Start
benchmarking at OWASP's minimum of 19 MiB, two iterations and parallelism one; choose
the actual work factor against measured latency and concurrent memory budgets.
Do not add the dependency in this documentation phase.

Define a documented login-identifier normalization rule backed by a unique constraint;
never normalize or silently truncate passwords. Bound password input size and hash
concurrency. Use the same public response for unknown user/wrong password/disabled
account and a dummy hash verification path for unknown users. This reduces obvious
enumeration signals; do not claim perfect timing equivalence.

Login throttling must cover unauthenticated clients and identifiers, separately from
the application's 2 calls/sec/user policy. Require a shared enforcement strategy before
multi-replica deployment, with bounded cardinality, trustworthy client-IP handling and
no permanent account lockout that attackers can trigger. Exact budgets/backend are OPEN
and must be settled before enabling credential verification publicly. The implemented
initial policy is documented in ../architecture/graphql-login.md: 60 global attempts
per minute, 20 per direct peer per minute, and 5 per identifier per fifteen minutes.
Trusted-proxy support remains deferred; forwarded headers are ignored.

## Authorization contract

- Roles USER and ADMIN are read from trusted server state, not cookie claims or input.
- No self-service admin promotion. Initial admin provisioning is an operator action;
  credentials must not enter migrations, source, process arguments or logs.
- Central helpers/focused services express CanReadFile, CanDeleteFile, CanShareFile
  and CanViewAdminData as those features arrive. Do not create empty policy packages.
- Owner-scoped SQL must enforce authorization at mutation time. A prior UI check or
  resolver lookup alone is insufficient. Recheck mutable grants inside transactions.
- Proposed policy: USER manages owned files; sharing grants reading only. ADMIN may
  view admin metadata/statistics but does not automatically receive private byte access
  or deletion rights. Additional admin powers require explicit documented requirements.
- Return safe typed UNAUTHENTICATED, FORBIDDEN and INVALID_INPUT errors. For inaccessible
  private file IDs, prefer a uniform NOT_FOUND response to avoid revealing existence.
- HttpOnly/CSRF controls do not solve stored XSS. Future preview policy remains required.

## Proposed GraphQL surface (not implemented)

| Operation | Purpose | Guard |
|---|---|---|
| mutation beginSession | Obtain session-bound CSRF token | Restricted bootstrap above |
| mutation login | Verify credentials and rotate session | Pre-auth cookie + CSRF + Origin |
| query me | Current user's safe profile and role | Authenticated session + CSRF + Origin |
| mutation logout | Revoke current session and clear cookie | Authenticated session + CSRF + Origin |

Return CSRF token plus safe user data after login; never return password hashes or
authentication cookie values. Mark all responses no-store. Exact SDL is deferred.
Only one login/logout/session-changing root field per operation is allowed; reject
combined/aliased attempts that would create ambiguous Set-Cookie or identity state.

## Migration and integration plan

Future additive migration(s) will add credentials/identity mapping, user role/status,
and sessions. Anonymous session rows need nullable user ownership with clear lifetime
constraints. Grant runtime privileges explicitly; no runtime DDL or admin connection.
Preserve users.id and existing file ownership. Do not retrofit migration 000001.

Future config covers public Origin, environment/cookie mode, timeouts and hashing/abuse
budgets. Dependency injection will pass an auth service into resolvers/middleware.
The existing error presenter must admit narrowly typed public errors without exposing
arbitrary error messages. No parallel REST login endpoint is proposed.

## Acceptance criteria before calling authentication complete

- Cookie flags and production startup validation tested, including deletion scope.
- Unknown users, bad credentials and disabled users return the same safe response.
- Login rotates tokens; old anonymous and authenticated tokens cannot be replayed.
- Expired/revoked sessions fail across two API instances using the same PostgreSQL.
- Logout racing activity renewal cannot resurrect a session; concurrent login is tested.
- Cross-site, missing/null Origin, missing/wrong token and cross-session token fail.
- Bootstrap cannot execute protected aliased/fragmented fields or multipart uploads.
- Login and logout CSRF fail; no state change via GET; batch arrays remain rejected.
- Auth changes cannot be combined in one operation to confuse identity/cookie state.
- USER cannot invoke admin actions; changing a client-supplied role has no effect.
- Later ownership-sensitive features test cross-user access and mutation every time.
- Database failure fails closed; responses and captured logs contain no credentials.
- PostgreSQL integration tests cover constraints, uniqueness, expiry and revocation;
  GraphQL tests cover safe errors; browser tests eventually prove real cookie behavior.

## Review result and remaining decisions

Design addresses login CSRF with a protected anonymous bootstrap, fixation with rotation,
and replica consistency with database-backed sessions. Complexity cost is additional
session reads/writes and a carefully constrained bootstrap exception.

Identity source/account provisioning is now accepted. Before HTTP authentication implementation, settle auth abuse
budgets, and confirm admin powers. Lifetime defaults remain configurable hypotheses.
The original session proposal was reviewed without runtime tests. The credential-only
follow-up and its actual validation are recorded in ../architecture/credentials.md;
session persistence, pre-login rotation and the browser boundary are now implemented
and tested. The operation-aware beginSession mutation and shared allocation budget
are described in [session bootstrap](../architecture/session-bootstrap.md).
GraphQL login, shared throttling, authenticated me and logout are now implemented.
See ../architecture/graphql-logout.md for revocation and retry semantics.
Real-browser acceptance and deployment abuse validation remain outstanding.

## References

- [OWASP session management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP CSRF prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
- [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)

These sources guide controls; application defaults and the proposed protocol above
are our design choices, not statements of assignment requirements.
