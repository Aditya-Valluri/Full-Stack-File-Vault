# File Vault Application - secure and Production-Grade

A secure file vault being built incrementally with Go, PostgreSQL, and a planned
React/TypeScript frontend. GraphQL is the single application API architecture.

## Current implementation

- PostgreSQL schema separates shared SHA-256 content metadata from user-owned files.
- Versioned migrations and a restricted runtime database account.
- Go foundation with chi, pgxpool, configuration validation, JSON logging,
  health/readiness probes, and graceful shutdown.
- Local Docker Compose database and a PowerShell API bootstrap script.
- gqlgen at `/graphql` with a public `serviceInfo` query and bounded JSON POST requests.

This is a foundation in active development, not a completed production deployment.
Authentication/authorization, multipart uploads, content hashing/storage,
quota enforcement, strict per-user rate limiting, and the frontend are upcoming steps.

## Run locally

Install Go 1.27+ and Docker Desktop with its Linux engine running. Configure the
root `.env` with a local PostgreSQL password using `.env.example` as a reference.
From the repository root in PowerShell:

```powershell
./scripts/start-api.ps1
```

The script starts PostgreSQL, applies migrations, provisions an ignored local
runtime credential, and starts the API at `http://127.0.0.1:8080`.
Operational probes are `/healthz` and `/readyz`. The initial GraphQL query and its
review are documented in the [Step 3 guide](docs/architecture/step-3-graphql.md).

## Validation

From `apps/api`:

```powershell
go test ./...
go vet ./...
go build ./...
```

See the [Go foundation guide](docs/architecture/step-2-go-foundation.md) for details.

## Architecture decisions

- [Content deduplication](docs/decisions/0001-content-deduplication.md)
- [Quota accounting](docs/decisions/0002-quota-accounting.md)
- [GraphQL-only application API](docs/decisions/0003-graphql-application-api.md)

The Compose project identifier remains `balkanid-vault` to preserve existing local
container and volume names. The project display name is the title above.
