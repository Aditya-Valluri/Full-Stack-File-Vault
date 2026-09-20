# Authorized content access, deletion, cleanup, and storage statistics

## WHAT

This increment implements ten connected pieces: safe storage read handles; hashed
access-grant migrations; GraphQL grant creation; authenticated HTTP byte streaming;
restricted image previews; logical deletion and quota release; published-blob GC;
durable orphan-intent recovery; a separately privileged worker; and owner-only statistics.
Application operations remain exclusively GraphQL. GET/HEAD /content/{opaque-token}
only transport bytes after authorization; there is no REST application API.

## WHY & TRADE-OFFS

| Choice | Selected approach | Alternative and trade-off |
|---|---|---|
| Reading content | Rooted, verified seekable file handles | Returning physical paths couples transport to storage and exposes identifiers |
| Access grants | Random 256-bit tokens, hashed in PostgreSQL, bound to issuing session | Signed stateless URLs reduce DB work but make immediate revocation harder |
| Authorization API | GraphQL mutation issues a relative URL | Base64 in GraphQL inflates payloads and prevents ordinary byte streaming |
| Byte transfer | net/http ServeContent, GET/HEAD, one byte range | Buffering an entire file costs memory; multipart ranges add unneeded work |
| Preview policy | Detected PNG/JPEG/WebP only, sandbox and nosniff | Arbitrary inline HTML/SVG/PDF requires a stronger isolated viewer policy |
| Logical deletion | Transactional file removal, grant cascade, full quota release | Immediate unlink risks deleting bytes shared by another file |
| Published cleanup | Commit generation retirement before exact-key removal | Holding a DB lock during unlink is insufficient if the transaction is lost |
| Orphan recovery | Durable DELETING fences retained and periodically retried | Age-only directory sweeping cannot establish that a writer has stopped |
| Worker | Separate vault_gc role, foreground managed process | In-process cleanup couples API availability and destructive privileges |
| Statistics | Owner-only logical and distinct-content totals | Global physical totals reveal other users' activity and include GC overhead |

## THE HOW

### Layout

- db/migrations/000010_file_access.*: grants, expiry/lookup indexes, runtime deletion.
- db/migrations/000011_cleanup_worker.*: retry schedule and narrow worker role.
- apps/api/internal/upload/objects.go: rooted Open/Remove operations.
- apps/api/internal/auth/transport.go: content browser/session boundary.
- apps/api/internal/files/{access,content,lifecycle}.go: grants, streaming, deletion/stats.
- apps/api/internal/cleanup/: retirement, orphan fencing, retry loop.
- apps/api/cmd/collect/: separately configured worker.
- apps/api/internal/graph/schema.graphqls: public contract; generated Go bindings.
- scripts/start-gc.ps1: local-only credential bootstrap and worker launcher.
- apps/api/cmd/provision-user/{lifecycle,cleanup}_integration_test.go: database tests.

Apply migrations through the existing local launcher, or from the repository root:

~~~powershell
docker compose run --rm migrate
./scripts/start-gc.ps1 -Once
~~~

Run ./scripts/start-gc.ps1 without -Once for foreground periodic cleanup. Ctrl+C
cancels scheduling/current database work; the worker closes storage and its pool
after the current cycle returns. The script provisions an ignored .secrets/gc-password,
passes it to psql through stdin, and never prints a DSN. API and worker must use the
same BLOB_STORAGE_DIR and coherent volume. A custom relative path is resolved from
apps/api in both launchers. Local Compose uses the existing database volume.

For deployment, build cmd/collect and run it under a supervisor. Set GC_DATABASE_URL
to a separately provisioned vault_gc login and BLOB_STORAGE_DIR explicitly; use
APP_ENV=production (the default). Configure verified database TLS through the DSN.
The worker rejects any other database current_user, including an administrator.
Production uses the same private, pre-existing Linux storage directory as the API.
Defaults are batch=20 (1..100), interval=30s (1s..1h), two DB connections, and a
30-second cycle context. Use -batch, -interval, and -once to override. Each phase is
bounded by batch size. Filesystem syscalls depend on a healthy local volume and are
not forcibly interrupted by a Go context.

Migration rollback is intentionally guarded: 000010 refuses outstanding grants;
000011 refuses DELETING tombstones. Stop API and worker before operator-managed
rollback. Do not discard recovery records just to make a rollback succeed.

### GraphQL and browser contract

~~~graphql
mutation Access($id: ID!) {
  createFileAccess(fileId: $id, mode: DOWNLOAD) { url expiresAt }
}
mutation Preview($id: ID!) {
  createFileAccess(fileId: $id, mode: PREVIEW) { url expiresAt }
}
mutation Delete($id: ID!) { deleteFile(id: $id) }
query Statistics {
  storageStats { fileCount logicalBytes uniqueContentBytes savedBytes savingsPercent }
}
~~~

Send mutations through the existing authenticated POST /graphql boundary with
Origin, session cookie, and X-CSRF-Token. Only one mutation root is permitted.
The returned relative URL can be used by a same-origin anchor or image. A copied
URL alone does not grant access: redemption requires the exact issuing session,
a still-enabled owner, an unexpired grant, and a still-owned logical file.

Grants last at most 60 seconds, further bounded by current session expiry. At most
64 unexpired grants exist per user; expired grants are reclaimed on issuance and
by the worker. PostgreSQL stores only the SHA-256 token digest. URLs are sensitive:
do not log full request paths in a reverse proxy or analytics system.

Content requests accept GET/HEAD, reject cross-origin Origin or cross-site Fetch
Metadata, require the session cookie, and use the existing shared user rate limiter.
They do not require a CSRF header because they only read already-authorized bytes.
Issuing a grant and retrieving bytes each consume one admitted request. Preview
galleries must schedule requests within the default two-per-rolling-second limit.

Downloads force application/octet-stream and attachment. Preview is allowed only
for detected PNG/JPEG/WebP; MIME sniffing is not full image validation or malware
scanning. HTML, SVG, PDF, and other formats remain attachment-only. Responses use
private/no-store, nosniff, no-referrer, same-origin resource policy, and a sandboxed
CSP. No hash ETag or physical key is exposed. Single ranges and HEAD are supported;
multipart ranges are rejected. Existing server transfer timeouts apply.

Authorization is rechecked and a file handle opened while holding the owner lock.
The transaction ends before streaming. An already-open transfer may complete after
deletion/session revocation; subsequent opens fail. This avoids holding DB locks for
a slow browser. The Linux adapter permits an open handle to finish after unlink;
Windows development can defer removal until handles close.

### Deletion and cleanup invariants

Deletion locks user, session, digest advisory key, then blob. It removes the owned
logical row, cascades its grants, and subtracts its full logical quota charge in one
transaction. A repeated/missing/foreign ID returns NOT_FOUND. Counter underflow fails
closed and rolls back. Only the final reference schedules GC_PENDING with a one-minute
grace period. Indexed file references, not a cached ref_count, remain authoritative.

The collector obtains the same digest lock, rechecks references, records DELETING
for the exact generation, deletes unreferenced blob metadata, and commits. Only a
successful commit permits physical removal. A new upload of identical content after
retirement publishes a different random generation. A failed/ambiguous retirement
commit never permits eager deletion.

Orphan intents become eligible immediately in CLEANUP or after ten minutes in PENDING.
Under digest/blob/intent locks the worker verifies that no committed blob references
the key, then commits DELETING before I/O. Age is only eligibility, not ownership proof.
Publication rejects a fenced intent. If a writer loses its DB transaction and finishes
promotion late, the retained tombstone schedules another exact-key removal.

Failures retry after 30 seconds; successful removals (including already absent keys)
retry after one hour to catch late I/O. Tombstones are deliberately retained indefinitely.
Monitor their growth and cleanup errors; safely compacting them requires an additional
writer-lifetime/recovery proof. Reported removed counts are completed cleanup attempts,
not physical bytes reclaimed. Concurrent workers use locks and idempotent removal.

Normal request cleanup closes temporary staging/prepared files. Crash-abandoned .part
and .candidate-* temporary files are NOT covered by final-generation intent recovery.
A future staging lease/ownership design is required; no unsafe age-only recursive sweep
is provided. Storage exhaustion, power-loss, and volume-driver validation remain
deployment tests. Upload request idempotency is also a separate follow-up.

### Statistics semantics

Counts and byte totals are decimal strings to preserve bigint precision. Savings
percentage is a two-decimal string. For two owned references to identical 4-byte
content: fileCount=2, logicalBytes=8, uniqueContentBytes=4, savedBytes=4, savingsPercent=50.00.
Another user's references do not affect these values. Empty logical usage yields
0.00 percent. These totals are neither quota discounts nor actual disk-usage telemetry:
staging, retired generations, and global cross-user reuse are deliberately excluded.

## VALIDATION

Unit tests verify rooted reads/removal, transport session checks, and preview policy.
Real PostgreSQL/TLS tests verify grants, expiry/caps, session and owner isolation,
ranges/HEAD, restrictive headers, preview rejection, deletion rollback, quota release,
shared references, and stats. Cleanup tests cover role separation, delayed removal vs
new publication, referenced-candidate protection, held digest locks, failed retirement,
lost removal acknowledgement, late orphan promotion, and concurrent workers.
Migration up/down round trips run in the disposable database.

References: [Go ServeContent](https://pkg.go.dev/net/http#ServeContent),
[PostgreSQL locks](https://www.postgresql.org/docs/17/explicit-locking.html),
[CSP sandbox](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/sandbox).

## ACTIONABLE MOMENTUM

Choose one next increment: (1) sharing with explicit access/revocation policy,
or (2) authorized administration with auditable operations.
