# GraphQL logout

## WHAT

`Mutation.logout: Boolean!` revokes the current authenticated session in PostgreSQL
and expires its cookie after revocation succeeds. It affects only this session;
other devices/sessions for the same account remain valid.

## WHY & TRADE-OFFS

Deleting the browser cookie alone cannot invalidate a copied credential. Database
revocation rejects future authorization checks across API instances, at the cost of
requiring PostgreSQL availability. On a database error, return a generic failure and
retain the cookie so the client can retry. Never report successful logout without a
successful revocation operation. If the response is lost after the database commits,
the credential is already invalid; retrying with it returns 401 and clears the cookie.

Revoke is idempotent internally. Two requests authorized before revocation may both
succeed. A subsequent request with the revoked cookie fails at the browser boundary
with UNAUTHENTICATED/401 and a clearing cookie. This transport behavior deliberately
does not promise that every repeated GraphQL logout returns true.

Revocation does not cancel reads already authorized before logout. Session renewal
rechecks revoked state under a row lock, so activity cannot resurrect the session.
Sensitive writes must still recheck authorization in their own transaction.

## THE HOW

- internal/auth/logout.go: authenticated-context guard and logout capability.
- internal/auth/browser.go: binds the current cookie to revocation; clears it only
  after success. Resolvers never receive the raw credential.
- internal/graph/schema.graphqls and schema.resolvers.go: expose logout through the
  existing GraphQL transport, with no separate REST route or new migration.
- cmd/server/main.go: injects SessionStore.Revoke into the authentication handler.

```graphql
mutation SignOut {
  logout
}
```

Send the authenticated cookie, current X-CSRF-Token and configured Origin. Logout
must be the sole mutation root; aliases/fragments are allowed, but mixed/repeated
roots and root directives are rejected before execution. The bootstrap header never
grants logout capability. Anonymous/cookie-less calls receive UNAUTHENTICATED.

Success returns `{ "data": { "logout": true } }` and an expired cookie with the
same name, Path=/, SameSite=Lax, HttpOnly and Secure settings as issuance, no Domain,
an empty value and Max-Age=0 on the wire. Explicit loopback development uses the
existing non-Secure development cookie. Responses remain no-store.

The browser should discard its in-memory CSRF token and user state on confirmed
logout or UNAUTHENTICATED. Bootstrap again before the next login. Do not loop retries
on storage failure or claim success merely because local UI state was cleared.

## VALIDATION

Unit tests cover authenticated/anonymous requests, CSRF, bootstrap bypass, aliases,
fragments, mixed/repeated mutations, revocation errors and exact cookie deletion scope.
PostgreSQL/TLS integration covers injected revoke failure after authorization, retained
cookie on failure, concurrent activity, cross-pool replay rejection and preservation
of another session. Existing deterministic row-lock tests verify a waiting renewal
cannot revive a revoked session. Real-browser acceptance remains outstanding.
Unit tests and the full isolated PostgreSQL/TLS suite passed (24.261 seconds).

## ACTIONABLE MOMENTUM

Next: browser authentication walkthrough, or strict per-user two-calls-per-second API
limiting. Login attempt budgets do not implement the latter requirement.
