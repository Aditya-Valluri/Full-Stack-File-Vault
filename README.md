# Full Stack File Vault

A secure file vault built incrementally with Go, PostgreSQL, and gqlgen.
GraphQL is the single application API, with a React/TypeScript browser frontend.

## Current implementation

- SHA-256 shared blobs separated from user-owned logical files.
- Versioned migrations and restricted runtime database credentials.
- chi/pgxpool server with health/readiness probes and graceful shutdown.
- Operator-provisioned accounts, Argon2id passwords, HttpOnly sessions, login/logout,
  current-user query, exact Origin and CSRF checks.
- Bounded GraphQL single/batch multipart uploads with immutable local generations.
- Transactional logical quota, duplicate charging, atomic batch publication, and
  usage reconciliation.
- PostgreSQL-backed per-user admission: default two calls per rolling second across replicas.
- GraphQL quota query; upload responses reveal no content hashes or deduplication hints.
- Owner-only file metadata, private tags, cursor pagination, and combined filename/MIME/size/date/tag/uploader search.

- Session-bound short-lived download URLs, streaming/ranges, and restricted image previews.
- Transactional logical deletion, quota release, and grant revocation.
- Separately privileged GC with durable orphan-intent recovery and exact-generation retries.
- Owner-only logical storage and deduplication statistics.

- Expiring public/account-restricted sharing with revocation and shared download tracking.

- Role-protected administration, quota controls, and append-only audit records.
- React/TypeScript UI with authentication, uploads, search, previews, sharing, and administration.
- Owner-scoped upload retry receipts and lease-aware abandoned temporary-file cleanup.
- Non-root application containers, HTTPS rehearsal, CI, and Kubernetes deployment templates.
- Private Prometheus metrics, alert rules, encrypted paired backups, and isolated restore checks.

Local browser, integration, and container rehearsals validate the implementation.
Production cluster rollout, private object-storage backup upload, notification routing,
and recovery objectives still require deployment-specific configuration and acceptance.
See [frontend](docs/architecture/frontend.md), [recovery](docs/architecture/recovery.md),
[deployment](docs/architecture/deployment.md), and [operations](docs/architecture/operations.md).

## Hiring demo on Render

The root Blueprint now requests Free web and PostgreSQL services for a temporary
hiring demo. Free Render cannot attach a persistent volume; the explicitly selected
`BLOB_STORAGE_BACKEND=postgres-demo` adapter stores bounded file contents in
PostgreSQL so app sleep/restarts do not break downloads. Each file is limited to
10 MB, shared physical content to 100 MB/10,000 objects, and the free database
expires 30 days after creation. The UI labels the environment as temporary.

The default production adapter remains the existing Linux filesystem store;
durable external object storage is the production target. This demo adapter does
not replace production storage or provide permanent retention/high availability.
See [ADR 0006](docs/decisions/0006-temporary-postgres-demo-storage.md) for the
decision, size/concurrency guards, privilege boundaries and recovery protocol.

Follow the [free demo guide](docs/render-demo.md) to deploy a fresh Blueprint,
check billing/usage controls, retrieve private reviewer credentials and verify
the assigned URL. No public deployment has been verified yet. Free resource plans
alone do not prevent account-level overage charges if payment is enabled.
Any paid resources from earlier attempts remain until explicitly removed.

## Run locally

Install Go 1.27+ and Docker Desktop with its Linux engine running. Configure the
root `.env` with a PostgreSQL password using `.env.example` as a reference.
From `D:\File Vault` in PowerShell:

```powershell
./scripts/start-api.ps1
```

The script starts PostgreSQL, applies migrations, provisions an ignored runtime
credential, and starts the API at `http://127.0.0.1:8080`. Development upload storage
defaults to ignored `apps/api/data/staging` and `apps/api/data/blobs`.
Export API settings explicitly; the Go process does not read `.env`.

Operational probes are `/healthz` and `/readyz`. Application operations use
`POST /graphql`. See the [upload guide](docs/architecture/upload-publication.md)
for configuration, multipart examples, quota semantics, storage assumptions, and limits.
File queries and filter semantics are in the [file query guide](docs/architecture/file-queries.md).
Downloads, deletion, statistics, and worker setup are in the
[file lifecycle guide](docs/architecture/file-lifecycle.md). Run
`./scripts/start-gc.ps1 -Once` for one cleanup cycle, or omit `-Once` for a foreground worker.
API and worker must share the same blob directory.
Sharing policy and examples are in the [sharing guide](docs/architecture/sharing.md).
Authentication setup is described in the [login guide](docs/architecture/graphql-login.md).

## Validation

From `apps/api`:

```powershell
go test ./...
go vet ./...
go build ./...
go test -tags integration ./cmd/provision-user -run TestProvisionCommandIntegration -count=1
```

Integration tests create a disposable PostgreSQL container and never target the
development database. Linux durable-storage validation is separate from Windows development.

## Architecture decisions

- [Content deduplication](docs/decisions/0001-content-deduplication.md)
- [Quota accounting](docs/decisions/0002-quota-accounting.md)
- [GraphQL-only application API](docs/decisions/0003-graphql-application-api.md)
- [Browser authentication](docs/decisions/0004-browser-authentication.md)
- [Quota and recoverable publication](docs/decisions/0005-quota-and-publication.md)

The project is named **File Vault**. New installations use the Compose project
identifier `file-vault`; the Go module is `file-vault.local/api`.

Existing checkouts retain their previous Compose project identifier in the ignored
`.env` as `COMPOSE_PROJECT_NAME` to reuse their database container and volume.
Changing the folder name does not require migrating database data.
