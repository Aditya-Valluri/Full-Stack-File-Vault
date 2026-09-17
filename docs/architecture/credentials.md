# Operator-provisioned credentials

## Scope

Migration 3 adds USER/ADMIN roles and disabled_at to users and a separate credentials
table. Existing users remain intact and have no login until separately provisioned.
The current command creates new accounts only; it does not reset passwords, attach
credentials to existing users, promote users, or implement authentication/session APIs.

Username policy is an ADR: 3-64 ASCII characters, first alphanumeric, subsequent
characters alphanumeric, dot, underscore or hyphen. Normalize surrounding whitespace
and ASCII capitals before insertion. These are usernames, not verified email addresses.
PostgreSQL checks canonical syntax and enforces uniqueness with C collation.

Passwords require at least 15 Unicode characters and at most 1024 UTF-8 bytes.
No trimming or normalization. Hashing uses golang.org/x/crypto Argon2id with a random
16-byte salt, 32-byte output, 19 MiB memory, two iterations and one lane. This initial
profile follows the [OWASP minimum](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
Benchmark and bound concurrent verification before exposing login. The PHC-style
encoding stores the profile; verification accepts only that exact profile, so malformed
database values cannot request arbitrary CPU/memory. New profiles require explicit
support and a rehash policy. No custom cryptographic primitive is implemented.

## Run

Apply pending migrations using the existing local admin configuration:

```powershell
docker compose run --rm migrate
```

Supply `PROVISION_DATABASE_URL` in the operator's process environment through a local
secret mechanism. It must use the migration/admin account, not vault_runtime. Do not
paste real credentials into tracked files or shell commands/history. Production DSNs
must verify TLS; the existing local database allows loopback-only development access.

With Go on PATH, run from the repository root:

```powershell
./scripts/provision-user.ps1 -Login reviewer -Role USER
```

The helper builds an ignored executable and prompts with hidden password input. It
passes exact UTF-8 bytes through stdin without a trailing newline. ADMIN provisioning
requires an explicit `-Role ADMIN`. No default account/password is shipped.
Clear the operator DSN environment variable after use. The helper briefly converts
the password into managed memory; it does not promise complete memory erasure.

The Go command refuses interactive terminal input. For other operating systems use
a trusted secret source writing exact bytes to its stdin, never a password CLI flag.
Do not use a line-oriented pipe that appends a newline unintentionally.

Creation is transactional: duplicate canonical names roll back the new user and never
change existing credentials. Unexpected database messages and hashes are not printed.
The runtime role can read credentials for future login but cannot insert/update/delete
credentials or change user roles. SELECT privilege is not tenant authorization.

## Validation and limits

Unit tests cover distinct salts, matching/wrong passwords, whitespace significance,
size/UTF-8 rejection, invalid hash encodings, hostile cost parameters and login rules.
Sessions, CSRF, login throttling, dummy verification for unknown users, role enforcement,
password reset and public registration are not implemented by this micro-step.
The down migration destroys credentials and role/status metadata; use only in a
disposable test environment. Never rollback a production identity store casually.

## Validation recorded 2026-09-16

- gofmt applied; `go test -count=1 ./...`, `go vet ./...` and `go build ./...` passed.
- PowerShell provisioning helper parsed without errors; hidden interactive prompting
  was not exercised by automation.
- Migration 3 applied to local PostgreSQL; schema_migrations reports 3, dirty=false.
- `db/tests/credentials.sql` passed with all test data rolled back. It checks role
  defaults/constraints, canonical/duplicate logins and restricted runtime privileges.
- No persistent reviewer/admin account was created; choose credentials interactively
  when provisioning. Initially SQL constraints and password primitives were tested
  separately; the command-level follow-up below now covers their integration.

Re-run the database assertions from the repository root:

```powershell
Get-Content -Raw db/tests/credentials.sql | docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U vault_admin -d vault
```

Substitute database/user names if local Compose configuration differs.

## End-to-end provisioning verification

Run from `apps/api` with Go and Docker on PATH and the Linux engine running:

```powershell
go test -tags integration -run TestProvisionCommandIntegration -count=1 -v ./cmd/provision-user
```

The test creates a separate PostgreSQL 17 container on a random loopback port,
executes migrations 1-3, builds and invokes the actual provisioning executable,
and removes its own container and anonymous volume afterward. It does not use the
development `.env`, runtime secret file or Compose database. It requires Docker
permission and may download the PostgreSQL image if not already cached.

Verified on 2026-09-16: PASS (28.46 seconds). Both USER and ADMIN account creation,
stored-password verification, exact trailing whitespace, canonical username handling,
duplicate rollback without orphan users, preservation of existing role/hash, runtime
provisioning denial and command-output redaction passed. Cleanup completed successfully.
The hidden-input PowerShell UI is still not automated; this verifies the executable's
stdin path, not an interactive terminal prompt. Forced process termination can bypass
test cleanup; any leftover container has the `vault-credentials-test-` prefix.
