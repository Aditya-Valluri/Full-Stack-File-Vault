# Step 2: Go HTTP and PostgreSQL foundation

Current startup also requires the [browser security configuration](browser-security.md).
The local launcher supplies development defaults; direct Go execution requires an
explicit PUBLIC_ORIGIN and defaults APP_ENV to production.

## What

The API module is `balkanid.local/vault/api` under `apps/api`. It uses chi and pgxpool
with JSON logs, startup validation, bounded readiness checks and graceful shutdown.
The local module path can be renamed when the repository's canonical remote exists.
GraphQL is settled as the sole application API; its implementation is Step 3.

## Why and trade-offs

chi composes standard net/http handlers and will host gqlgen without a second
application API. pgxpool is PostgreSQL-specific, unlike database/sql, and provides
explicit pool lifecycle control. Ten connections per process is an initial budget;
deployment must budget the total across replicas against PostgreSQL capacity.

The runtime role has SELECT on the three foundation tables and schema USAGE. It
has no application writes, role creation, database creation or superuser rights.
It inherits normal PUBLIC privileges. It is not a tenant isolation boundary;
authentication and owner-scoped authorization are still required before application
operations. Add future grants explicitly with the operations that need them.

The migration creates a cluster-scoped NOLOGIN role. Local bootstrap enables login
and assigns an ignored random password outside SQL migrations. One vault deployment
per PostgreSQL cluster is assumed; do not replay role creation into a second database
of the same cluster. Production provisioning must manage credentials separately.

## How

Prerequisites: Go 1.27+, Docker Desktop's Linux engine, and the root administrative
`.env` from Step 1. In a fresh PowerShell terminal at the repository root:

```powershell
./scripts/start-api.ps1
```

This starts PostgreSQL, applies pending migrations, provisions the runtime password
through stdin, and starts the API on 127.0.0.1:8080. It preserves the admin password.
Local runtime credentials live in `.secrets/runtime-password`, ignored by Git. The
script resets the runtime password to that value when rerun; it is local-only.

The Go program reads process environment only; it does not implicitly load `.env`.
`DATABASE_URL` is required. `HTTP_ADDR` defaults to `127.0.0.1:8080`. TLS is disabled
only in the local bootstrap connection; deployment must supply a verified TLS DSN.

In another terminal:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz
```

Expected status: `ok` and `ready`. Ctrl+C initiates graceful shutdown with a 15-second
drain deadline. Liveness does not query PostgreSQL; readiness checks the runtime role
and table accessibility with a 2-second deadline and returns generic failure details.

Run checks from `apps/api`:

```powershell
go test ./...
go vet ./...
go build ./...
```

HTTP read/write limits are initial operational defaults. Revisit them explicitly
when implementing multipart uploads. No GraphQL, auth, uploads, rate limiter or
quota enforcement is implemented in this step.

## Validation recorded on 2026-09-14

- `go mod tidy`, `go test ./...`, `go vet ./...` and `go build ./...` passed.
- Migration 2 applied; PostgreSQL reported version 2 and dirty=false.
- Runtime role has SELECT but no INSERT on vault.files and no CREATE on vault.
  Superuser, database creation, role creation and RLS bypass flags are all false.
- Local HTTP checks returned healthz=ok and readyz=ready using the runtime account.
- `.env` and `.secrets/runtime-password` are ignored by Git.
- Shutdown draining is covered by an HTTP test; Windows Ctrl+C was not separately
  exercised against the running development process.
