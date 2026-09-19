# Step 3: Initial GraphQL API and schema review

## What

gqlgen v0.17.95 serves the application API at `/graphql` through chi. The initial
public operation returns the project display name; it does not access PostgreSQL.
Application startup still requires the runtime database connection from Step 2.

## Why and trade-offs

Schema-first generation makes the contract explicit and generates typed Go models
and resolver interfaces. The alternative of handwritten GraphQL execution avoids
generated files but requires substantially more execution and validation machinery.
Generated files are intended to be committed; the generator version is pinned as a Go tool.

The only initial transport is JSON POST. Unlike enabling gqlgen's default transports,
this avoids exposing GET execution, multipart uploads or WebSockets before their
associated requirements are implemented. Multipart remains the chosen future upload
transport; this step does not implement file handling or authentication.

## Schema review

```graphql
type Query {
  serviceInfo: ServiceInfo!
}

type ServiceInfo {
  name: String!
}
```

- `Query.serviceInfo` is explicitly public and contains no user data.
- An object return type permits adding fields later without replacing a scalar
  return type. No speculative future fields are introduced now.
- Both fields are non-null because the current static display name is always
  available. Future fallible fields should choose nullability deliberately.
- `name` is display text, not an identifier or a version compatibility contract.
- No build SHA, runtime version, credentials or dependency health are disclosed.
- `/healthz` and `/readyz` remain operational probes, outside the application schema.
- There are no mutation, subscription, upload or user/file fields in this step.
- Schema introspection is not enabled as a handler extension; use the committed SDL
  for this initial review. A future developer-tooling policy can configure it.

Review outcome: appropriate scope for verifying GraphQL wiring before authentication.
The sole public field does not need database access or a user identity. This does
not establish an authorization pattern for the protected operations added later.

## Request boundaries

- JSON POST only; other methods return 405 and other content types return 415.
- 64 KiB body limit, including unknown-length requests; excess returns 413.
- Parser limit: 4096 tokens. Query complexity limit: 100.
- Resolver execution context has a 5-second deadline. Future dependency calls must
  honor context cancellation; it is not a hard interruption of arbitrary Go code.
- Batch arrays are rejected. Multipart uploads are not enabled yet.
- Resolver errors and panics are replaced by a generic error without logging payloads.
- Existing server read/write deadlines still apply to request transport.

These are resource limits, not the planned 2 calls/sec per-user rate limiter.
Authentication, CSRF policy and rate limiting remain later implementation work.

## How

From `apps/api`, regenerate after editing `internal/graph/schema.graphqls`:

```powershell
go tool gqlgen generate
go test ./...
go vet ./...
go build ./...
```

Edit resolver implementations in `schema.resolvers.go`; do not hand-edit
`generated.go` or `model/models_gen.go`. gqlgen preserves implemented resolvers.

Start or restart the API with `./scripts/start-api.ps1` from the repository root.
An already running Step 2 process will not pick up the new route automatically.
In another PowerShell terminal:

```powershell
$body = @{ query = 'query ServiceInfo { serviceInfo { name } }' } | ConvertTo-Json
Invoke-RestMethod -Uri http://127.0.0.1:8080/graphql -Method Post -ContentType application/json -Headers @{ Origin = 'http://127.0.0.1:8080' } -Body $body
```

Expected GraphQL result:

```json
{"data":{"serviceInfo":{"name":"File Vault"}}}
```

Validation: Go tests, vet and build passed. Handler tests exercise valid queries,
invalid fields/syntax, malformed JSON, batch rejection, unsupported transports,
unknown-length oversized bodies, and complexity rejection. These tests run without
PostgreSQL; no live database integration was required for the static resolver.

## Error boundary follow-up

The central error presenter in `internal/graph/errors.go` treats resolver failures
as private, including wrapped errors and gqlerror values with forged protocol codes.
Clients receive `INTERNAL_ERROR` and `internal server error`, retaining the field
path. Logs record the category, not the underlying message or panic value.

Only known pre-execution parse, validation and complexity errors retain their useful
messages and codes. Other pre-execution errors receive `INVALID_INPUT` and a generic
message. This is necessary because the installed gqlgen JSON decoder can include
the complete malformed request body in its error. Unknown internal failures before
field execution currently share this generic input response; refine their typed
classification when additional execution extensions are introduced.

Transport errors use the same `errors[].extensions.code` convention:

| Condition | HTTP status | Code |
|---|---|---|
| Unsupported method | 405 | METHOD_NOT_ALLOWED |
| Unsupported content type | 415 | UNSUPPORTED_MEDIA_TYPE |
| Body exceeds 64 KiB | 413 | REQUEST_TOO_LARGE |
| Invalid body | 400 | INVALID_INPUT |
| Resolver failure | 200 with GraphQL errors | INTERNAL_ERROR |

No public domain-error framework or future auth/quota error types are added yet.
Explicitly typed public errors should be introduced with the business behavior that
needs them. Do not make arbitrary resolver messages public to obtain useful errors.

Validation on 2026-09-15: formatting applied; `go test -count=1 ./...`, `go vet ./...`
and `go build ./...` passed. New tests exercise real gqlgen execution with failing
resolvers and verify response/log redaction, field paths, transport codes and useful
validation details. No database mutation or live deployment was needed for this change.
