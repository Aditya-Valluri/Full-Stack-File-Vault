# Step 1 validation

Run from `D:\BalkanID File Vault` in PowerShell. Stop after any failed command.

## Prerequisites

Docker Desktop with Linux containers and a running engine is required.
Official instructions: https://docs.docker.com/desktop/setup/install/windows-install/

## Checks

```powershell
docker version
docker compose version
docker compose config --quiet
docker compose up -d --wait postgres
docker compose run --rm migrate
```

Quiet Compose validation avoids printing the expanded database password.
The migration should complete with exit code zero.

For the default database and user, inspect the migration version and tables:

```powershell
docker compose exec postgres psql -v ON_ERROR_STOP=1 -U vault_admin -d vault -c 'TABLE public.schema_migrations;'
docker compose exec postgres psql -v ON_ERROR_STOP=1 -U vault_admin -d vault -c '\dt vault.*'
docker compose exec postgres psql -v ON_ERROR_STOP=1 -U vault_admin -d vault -c '\d vault.blobs'
docker compose exec postgres psql -v ON_ERROR_STOP=1 -U vault_admin -d vault -c '\d vault.files'
docker compose run --rm migrate
```

If you configured another database/user, substitute those names in the inspection
commands. Expected: migration version 1 with dirty=false; tables blobs, files and
users; digest uniqueness and restrictive foreign keys; a second migration run
reports no change. These inspections do not replace behavioral constraint tests.

Do not run `docker compose run --rm migrate version`: it replaces the complete
service command, losing the configured database and migration-path options.
Inspect `schema_migrations` with psql instead.

The reverse migration drops vault metadata. Test it only in a separate disposable
database, never against a populated development or production database.

Stop services while preserving persistent database data:

```powershell
docker compose down
```

## Recorded status

- Foundation SQL and Compose inspected statically.
- Docker Desktop 4.90.0 installed successfully through WinGet.
- Compose configuration validation passed using the installed Compose executable.
- WSL reported not installed; the attempted WSL installation exited with code 1.
- Historical installation blockers above were resolved sufficiently to run Docker.
- User confirmed migration 1 successful before Step 2. On 2026-09-14, migration 2
  also applied successfully, and PostgreSQL reported version 2 with dirty=false.
- Aggregate quotas and authorization are not implemented by the schema.
