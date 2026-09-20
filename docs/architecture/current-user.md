# Authenticated current user

## WHAT

`Query.me: AuthenticatedUser!` returns exactly id and role for the current authenticated
session. It accepts no user ID, login name or role arguments. Anonymous/bootstrap state
is not an authenticated identity.

## WHY & TRADE-OFFS

The browser middleware already checks PostgreSQL session validity, current user status
and role after CSRF verification. The resolver uses that trusted context through
auth.RequireUser rather than issuing a redundant second query. The result represents
authorization at the request check; it cannot promise cancellation if revocation races
an already-authorized read. Later sensitive writes must check eligibility in their
own transaction. No client-supplied identity claims are trusted.

A non-null me field makes unauthenticated access an explicit UNAUTHENTICATED error.
A nullable field could instead represent signed-out state without errors; this API
chooses an explicit protected-query contract. GraphQL non-null propagation can make
the entire data result null when me is mixed with public fields and access is denied.

## THE HOW

- internal/auth/authorization.go: shared authenticated-user guard, rejecting anonymous
  state and unknown roles.
- internal/graph/schema.graphqls: me reuses the login payload's safe user type.
- internal/graph/schema.resolvers.go: maps trusted ID/role to the GraphQL model.

```graphql
query CurrentUser {
  me { id role }
}
```

Send the authenticated cookie issued by login and its new X-CSRF-Token, along with
the configured Origin. Missing/anonymous state produces UNAUTHENTICATED at HTTP 200;
invalid cookie returns 401, failed CSRF returns 403, and unavailable session storage
returns 503. Responses remain no-store. There is no parallel REST endpoint.

## VALIDATION

Unit tests cover missing/anonymous context and unknown roles. PostgreSQL/TLS tests
cover two separate user identities, aliases/fragments, mixed public/protected queries,
cross-session CSRF, role freshness, disabled/revoked sessions and database failure.
Unit tests and the combined isolated PostgreSQL/TLS suite passed (25.880 seconds).
Real-browser tests remain outstanding.

## ACTIONABLE MOMENTUM

Next: implement logout, or walk through bootstrap/login/me in a browser client.
