# Browser cookies and the GraphQL request boundary

## Scope

The server entry point wraps gqlgen at `/graphql` with `auth.BrowserSecurity`.
Probes remain outside this boundary. The beginSession mutation issues anonymous
cookies through the operation-aware bootstrap boundary. Login now rotates anonymous
state through [GraphQL login](graphql-login.md); me requires an authenticated identity.
Logout now revokes the current credential and clears its cookie after success; see
[GraphQL logout](graphql-logout.md).
Cookie-less requests can access the current public serviceInfo schema. Future
protected resolvers must require a nonempty validated UserID: this middleware alone
is not application or object-level authorization.

## Configuration and cookie policy

- APP_ENV defaults to production; only production/development values are accepted.
- PUBLIC_ORIGIN is required: one exact origin, without path, query, fragment,
  wildcard or credentials. Matching includes the scheme, hostname and port.
- Production requires HTTPS. Configure a trusted ingress to terminate TLS. Incoming
  Host/Forwarded headers never determine the allowed Origin or cookie flags.
- Plain HTTP requires explicit development mode, a loopback public origin and a
  loopback HTTP_ADDR. The flag does not permit a publicly bound HTTP server.
- HTTPS cookie: __Host-vault_session; Secure, HttpOnly, SameSite=Lax, Path=/, no Domain.
  Expires is the absolute session expiry; PostgreSQL remains authoritative for idle
  expiry. Deletion uses matching scope and Max-Age=-1.
- HTTP development uses vault_session_dev without Secure. Cookie scope does not
  isolate ports: avoid untrusted apps on the same local hostname.

The local launcher supplies development defaults only when process variables are
absent and restores them on exit. Go still does not load `.env`. Direct `go run`
requires explicit PUBLIC_ORIGIN. Prefer a same-origin Vite proxy when adding the UI.

## Checks before body parsing

1. Only POST: GET/OPTIONS return 405, with no permissive CORS response.
2. Require exactly one Origin equal to configured PUBLIC_ORIGIN. Missing, null,
   foreign and duplicate origins fail. CLI clients must send this header too.
3. If Sec-Fetch-Site is present, accept only same-origin or none. Cross-site,
   same-site and unknown/duplicate values fail. Missing metadata does not skip Origin.
4. With a session cookie, require a single 43-character X-CSRF-Token. Reject duplicate
   session cookies rather than picking an ambiguous identity.
5. Read session state without renewal and compare its CSRF token in constant time.
   Unknown, expired, revoked and disabled-user sessions fail.
6. Only after CSRF succeeds, renew authenticated state and recheck validity. Anonymous
   sessions retain fixed expiry. Failed CSRF does not extend session lifetime.
7. Attach validated session metadata to context. Empty UserID means anonymous.

Cookie-bearing requests require CSRF, including serviceInfo, except for the narrowly
validated bootstrap operation described in [session-bootstrap.md](session-bootstrap.md).
X-Vault-CSRF-Bootstrap: 1 alone cannot authorize another operation. Multipart requests inherit this boundary before
body parsing, but multipart transport itself remains disabled.

Invalid sessions return UNAUTHENTICATED/401 and clear the cookie. CSRF/Origin failure
returns FORBIDDEN/403. Session-store failure returns INTERNAL_ERROR/503 and does not
clear credentials. Method rejection returns METHOD_NOT_ALLOWED/405. Responses use the
GraphQL error envelope without raw database messages or secret values.

Session state is a snapshot. Later sensitive writes must recheck authorization in
their transaction. These controls neither replace authorization nor prevent XSS.

## Public query example

Start with `./scripts/start-api.ps1`, then use another PowerShell terminal:

```powershell
$body = @{ query = '{ serviceInfo { name } }' } | ConvertTo-Json
Invoke-RestMethod http://127.0.0.1:8080/graphql -Method Post -ContentType application/json -Headers @{ Origin = 'http://127.0.0.1:8080' } -Body $body
```

## Validation and remaining work

Unit tests cover cookie scope/deletion, configuration, exact Origin, Fetch Metadata,
duplicate cookies, CSRF mismatch, session context, rejection before body reads, no
renewal on rejection and storage errors. The isolated PostgreSQL integration suite
passed in 9.10 seconds. Its TLS HTTP subtest exercised chi, the boundary, real session
state and gqlgen, including cross-session CSRF mismatch, revocation and DB failure.
Unit tests, vet and build passed; launcher syntax also passed. No migration was needed.

These are Go HTTP tests, not real-browser/Playwright acceptance tests. Cookie behavior
in browsers, frontend integration and public-deployment validation remain outstanding.
Login/logout CSRF and shared login throttling are covered by the newer operation tests.

References: [OWASP CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
and [MDN cookie attributes](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie).
The exact Origin policy is an application ADR, not an assignment requirement.
