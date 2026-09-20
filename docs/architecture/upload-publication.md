# Upload publication, quota, and request admission

## WHAT

The application now exposes `uploadFile`, `uploadFiles`, and `quota` at
`POST /graphql`. Single/batch uploads use gqlgen-compatible multipart; there is no
REST upload route. Authentication, Origin/CSRF checks, and per-user admission precede
file parsing. Each successful logical file charges its full size, including duplicates.

SPEC (user request): SHA-256 content deduplication, configurable quota with a
10,000,000-byte default, and strict default admission of two calls per second.
ADR: interpret that rate as at most two admitted authenticated HTTP requests in
any rolling one-second window, shared across replicas. A batch upload is one request;
file-count, body, complexity, and disk-capacity limits separately bound its work.

## WHY & TRADE-OFFS

- A transactional `used_bytes` counter gives constant-work upload admission. A
  relational SUM avoids drift but requires repeated aggregation. `Publisher.Reconcile`
  compares both under the writer lock; it reports drift without automatically repairing it.
- PostgreSQL row/advisory locks coordinate replicas. Process-local mutexes/limiters
  would multiply allowances and cannot protect globally shared content.
- Immutable random generations permit recoverable external byte storage. Storing
  bytes in PostgreSQL would simplify atomicity but enlarge the database and backups.
- The limiter stores at most 100 timestamps in one row per user, not an unbounded event
  log. Redis would reduce database traffic at the cost of another required service.
- Byte counts are decimal GraphQL strings: `Int` is insufficient for configurable
  bigint quotas and JavaScript numbers cannot exactly represent every bigint.
- MIME is identified from a bounded prefix. The declared MIME is retained only as
  untrusted metadata. Generic vault storage accepts arbitrary bytes; MIME detection
  is not malware scanning, format-aware validation, or permission to preview inline.

## THE HOW

### Database and storage

Migrations 000008 and 000009 add quota counters, blob lifecycle/candidate intents,
and shared rate windows. Existing over-quota data blocks migration 000008 rather than
being deleted. Its down migration refuses nonempty file/blob/candidate tables.

Publication prepares and hashes candidate bytes before taking user/session locks.
It commits an intent before promoting any final generation, then locks user, session,
sorted digest advisory keys, blob rows, and intents. Quota and all batch file records
commit together. Filesystem durability precedes the database commit; it is not a
cross-system atomic transaction. A failed/ambiguous commit never triggers eager
deletion of a final generation.

The Linux adapter syncs file content, creates a non-overwriting hard link, and syncs
the parent directory. Windows is allowed only in explicit development mode, where
directory fsync is omitted. Production directories must already exist, be writable,
and be private to the service account. Use a coherent durable filesystem supporting
hard links/fsync. API replicas MUST see the same blob directory; independent disks
with a shared database are unsupported.

Prepared/staged handles are removed on normal success/failure. Candidate intents
survive publication failures. The [lifecycle worker](file-lifecycle.md) now recovers
final-generation orphan intents and collects unreferenced published blobs.
Crash-abandoned temporary staging/prepared files still require separate recovery.
Do not run an age-only recursive sweep or delete an object merely because a request
reported failure. Monitor disk usage and retain uncertain generations for recovery.

### Configuration

Export these variables in the API process; Go does not load `.env` automatically.

| Variable | Default | Supported range |
|---|---|---|
| USER_CALLS_PER_SECOND | 2 | 1..100; identical on every replica |
| UPLOAD_MAX_FILE_BYTES | 20000000 | 1..1073741824 |
| UPLOAD_MAX_REQUEST_BYTES | 21000000 | greater than file limit; at most 2147483648 |
| UPLOAD_MAX_FILES | 10 | 1..100 |
| UPLOAD_MAX_CONCURRENT | 4 | 1..128 per API instance |
| UPLOAD_STAGING_DIR | data/staging | private existing directory in production |
| BLOB_STORAGE_DIR | data/blobs | private existing directory in production |

Relative directories are resolved from the API working directory; the bootstrap
script runs from `apps/api`. Development creates them automatically. Upload receipt
plus execution has a 20-second deadline within the server's 30-second I/O limits.
JSON requests retain a separate 64 KiB limit.

Per-file limits and per-user quota are separate. Operators configure
`vault.users.quota_bytes` in PostgreSQL; it is not capped at 10 MB.
Changing an environment variable does not retroactively change existing user quotas.
The database rejects quotas below stored usage. Schema defaults govern newly
provisioned users. Keep operator changes separate from the restricted API credential.

With defaults, four active requests can stage at most approximately 84 MB of body
data; prepared copies can roughly double active storage. Crash orphans, existing
content, other instances, and filesystem overhead are additional. This is not a
global disk reservation.

### GraphQL contract

```graphql
mutation Single($file: Upload!) {
  uploadFile(file: $file) { id name sizeBytes detectedMIME createdAt }
}
mutation Batch($files: [Upload!]!) {
  uploadFiles(files: $files) { id name sizeBytes detectedMIME createdAt }
}
query MyQuota {
  quota { usedBytes quotaBytes remainingBytes }
}
```

Requests need the authenticated HttpOnly session cookie, exact Origin, and
`X-CSRF-Token`. The browser sets Origin and the multipart boundary. For a same-origin
browser client that has already logged in:

```javascript
const form = new FormData();
form.append("operations", JSON.stringify({
  query: "mutation($file:Upload!){uploadFile(file:$file){id name sizeBytes}}",
  variables: { file: null }
}));
form.append("map", JSON.stringify({ "0": ["variables.file"] }));
form.append("0", selectedFile);
const response = await fetch("/graphql", {
  method: "POST",
  credentials: "same-origin",
  headers: { "X-CSRF-Token": csrfToken },
  body: form
});
const result = await response.json();
```

Do not manually set Content-Type for FormData. Convert byte strings with `BigInt`
when exact arithmetic is needed. No hashes, storage keys, reuse flags, or another
owner's metadata are returned.

Upload batches are all-or-nothing for logical files and quota. Empty files are valid.
Mixed/repeated mutation roots, HTTP batching, mapping fan-out, and Base64 uploads
are unsupported. Invalid requests reaching authenticated admission consume the
request budget even when later rejected.

Normal rate rejection returns HTTP 429, `RATE_LIMITED`, and a rounded-up Retry-After.
Authenticated CSRF bootstrap also consumes this budget; its resolver returns a
GraphQL RATE_LIMITED error (HTTP 200). Anonymous bootstrap/login have separate shared
budgets. Storage/database admission failures fail closed. Public unauthenticated
queries are not covered by the per-user policy.

Do not automatically retry upload mutations after network/commit ambiguity:
deduplication does not make logical uploads idempotent. A retry can create another
logical file and charge quota again. Exactly-once retry semantics remain future work.

## VALIDATION

Tests cover migration backfill and rollback, runtime grants, local immutable
promotion and cleanup, altered source bytes, duplicate logical charging, zero-byte
uploads, atomic rejection, concurrent cross-pool quota/deduplication, authorization
rechecks, missing active content, pending-generation replacement, post-promotion
SQL/storage failures, reconciliation, shared rate admission, and TLS multipart/API
responses. The integration fixture owns and removes a separate PostgreSQL container.

From `apps/api` with Go and Docker available:

```powershell
go test ./...
go vet ./...
go build ./...
go test -tags integration ./cmd/provision-user -run TestProvisionCommandIntegration -count=1 -v
```

Linux storage tests exercise file and directory sync. They are not a power-loss test
or evidence that every volume driver meets durability requirements. GC-worker races
are now covered by the lifecycle integration suite. Ambiguous network commit drills,
storage exhaustion/load tests, and browser UI/UAT remain deployment/follow-up work.

Protocol and locking references:
[gqlgen uploads](https://gqlgen.com/reference/file-upload/),
[PostgreSQL locking](https://www.postgresql.org/docs/17/explicit-locking.html),
[Go rooted filesystem operations](https://pkg.go.dev/os#Root).

## ACTIONABLE MOMENTUM

File listing/search and logical deletion/GC are implemented. Next choices are sharing
or authorized administration; both must preserve ownership and the lock protocol.
