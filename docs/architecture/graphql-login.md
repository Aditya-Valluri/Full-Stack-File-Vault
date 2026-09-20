# GraphQL login

## WHAT

`login(input: LoginInput!): LoginPayload!` accepts loginName and password. It requires
an anonymous cookie plus its X-CSRF-Token, exact Origin, and acceptable Fetch Metadata.
It returns a fresh CSRF token and user { id role }. The new authentication credential
is issued only as a Secure HttpOnly cookie after the rotation transaction commits.

## WHY & TRADE-OFFS

Application ADR: reuse the PostgreSQL rotation service and add shared attempt budgets
before credential verification. In-memory counters allow separate budgets per replica;
Redis would avoid a PostgreSQL serialization point but adds a service and operational
work. PostgreSQL shares admission and expiry with the existing deployment.

Initial fixed policies (not assignment requirements):

| Scope | Attempts | Window |
|---|---:|---|
| Global | 60 | Database calendar minute |
| Direct peer IP | 20 | One minute from first admitted attempt |
| Normalized identifier | 5 | Fifteen minutes from first admitted attempt |

Each valid anonymous/CSRF request reaching login is charged, including successful
and failed credentials. Identifier checks apply equally to nonexistent accounts.
The global charge commits even when a narrower budget rejects the attempt; failed
rotation cannot erase the charge. Success does not reset budgets. Windows expire;
there is no permanent account lockout. Fixed windows permit boundary bursts, and
attackers can temporarily exhaust shared/identifier budgets. These are abuse guards,
not the separate strict two-calls-per-second authenticated-user limiter.

The global row serializes budget decisions. Only admitted attempts can allocate keys;
expired keys are deleted before new allocations under the same lock. At most two rows
are created per globally admitted attempt, with a maximum fifteen-minute active life.
Inactive expired rows remain until the next admission; unbounded new identifiers cannot
grow retained rows without passing the bounded global budget. Keys are SHA-256 hashes
of canonical peer addresses/identifiers, which are pseudonymous and dictionary-readable,
not secrets or anonymized data. Invalid identifiers share an impossible-username key.

Use the direct socket peer, canonicalizing IPv4-mapped IPv6. Ignore Forwarded and
X-Forwarded-For. Behind an ingress all clients may share the ingress peer budget;
trusted-proxy configuration and deployment-specific abuse controls remain necessary.

## THE HOW

- Migration 000007: runtime grants and global/peer/identifier budget tables.
- internal/auth/login_browser.go: budget reservation and browser login capability.
- internal/auth/browser.go: install capability only after anonymous Origin/CSRF checks.
- internal/graph/handler.go: reject mixed/repeated authentication mutation roots before
  execution, including aliased/fragmented roots. Root directives are also rejected.
- schema.graphqls and schema.resolvers.go: typed login input and safe user payload.

After bootstrap, send the anonymous cookie and X-CSRF-Token:

```graphql
mutation SignIn($input: LoginInput!) {
  login(input: $input) {
    csrfToken
    user { id role }
  }
}
```

Supply credentials through GraphQL variables over HTTPS, never URLs or process
arguments. Password bytes are not normalized; the supported maximum is 1024 bytes.
The temporary byte copy is cleared, but Go strings/JSON buffers are not guaranteed
securely erased. Do not log request bodies, credentials, cookies or CSRF values.

Wrong password, unknown account and disabled account yield identical UNAUTHENTICATED
responses. Rate rejection yields RATE_LIMITED. These resolver errors use HTTP 200;
Origin/CSRF transport rejection uses 403. Storage failures remain generic. Validation
and variable-coercion messages are redacted because they can include supplied secrets.
Clients should use error codes rather than parsing messages and must bound retries.

An authenticated cookie cannot log in again. Successful login consumes the anonymous
session and rotates both cookie and CSRF. Bootstrap never grants login capability.

## VALIDATION

Unit tests passed, including mixed/aliased/fragmented operation rejection, forged
forwarding headers, CSRF/Origin enforcement and secret-redacted validation errors.
The isolated PostgreSQL/TLS integration suite passed in 14.028 seconds: cookie rotation,
replay rejection, equal credential errors, multi-pool identifier concurrency, peer/global
limits, expiry recovery, rollback with retained charges and closed-database failure.
After adding me, the combined suite passed in 25.880 seconds. Migration 000007 was
applied to the local database.

Real-browser acceptance, deployment/load validation and account recovery remain outside
this step. Existing session expiry enforcement remains independent of cleanup jobs.

Reference: [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html).
