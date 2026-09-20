# ADR 0005: Transactional logical quota and recoverable content publication

Status: Publication, quota, logical deletion, published GC, and final-generation orphan recovery implemented. Temporary staging crash recovery remains planned.
Supersedes ADR 0002's proposed derived-usage enforcement, preserving its history and
per-logical-file accounting policy. User authorized this design step after staging.

## WHAT AND SOURCE CLASSIFICATION

SPEC (user's supplied assignment summary): configurable 10 MB default quota, content
deduplication, secure ownership and concurrent uploads. The original PDF is unverified.
Locked user ADR constraints: users.used_bytes maintained transactionally, authoritative
files-to-blobs references, opaque/random physical generations, PostgreSQL-coordinated
GC, durable cleanup intent, and storage-leak preference over loss of referenced content.

This ADR records the publication and recovery protocol. Migration 000008, the local
adapter, transactional Publisher, and GraphQL uploads now implement its publication
path. Migrations 000010/000011 and the lifecycle worker implement grants and cleanup.

## WHY & TRADE-OFFS

| Choice | Benefits | Costs / decision |
|---|---|---|
| SUM logical file sizes per upload | No counter drift | Repeated aggregation; superseded by the later explicit used_bytes requirement |
| Transactional used_bytes | Constant-work quota admission under a user lock | Every writer must maintain it; reconciliation required; selected |
| Store bytes in PostgreSQL | Content and metadata can commit atomically | Larger database/backups; differs from accepted external-content design |
| External immutable generations | Streaming and future object-storage adapter | Cross-system failures require durable recovery; selected |

Do not call filesystem and PostgreSQL operations one atomic transaction. Only database
metadata/usage changes are atomic together. Byte durability precedes their commit.

## QUOTA CONTRACT

- One logical reference charges the full blob size, including duplicates owned by
  the same user or another user. Two references to 4 MB charge 8 MB.
- Keep quota_bytes default 10,000,000 decimal bytes; higher operator-configured quotas
  remain valid. Neither multipart file limits nor temporary staging count as usage.
- Add used_bytes BIGINT NOT NULL with nonnegative and used_bytes <= quota_bytes checks.
  Lowering quota below existing usage is rejected; never delete files to make it fit.
- A batch upload is all-or-nothing for logical files and quota. Sum all submitted sizes
  with checked arithmetic, even if several files share a hash. Never partially report
  success in a failed batch. Zero-byte files charge zero but still consume file-count
  and request budgets.
- Under a user row lock, apply a guarded increment using subtraction to avoid overflow:

```sql
UPDATE vault.users
SET used_bytes = used_bytes + $2
WHERE id = $1 AND disabled_at IS NULL
  AND $2 >= 0 AND $2 <= quota_bytes - used_bytes
RETURNING used_bytes;
```

The service uses this guarded-update pattern inside its authorized transaction.
The caller supplies a trusted owner ID and a checked batch delta. Recheck the bound
session/current user in the same transaction; a prior middleware snapshot is insufficient.
Require exactly one updated row. Failure rolls back references and counter changes.

Deletion locks the same user, verifies ownership, deletes logical references and
subtracts their full sizes in one transaction. Require used_bytes >= delta; a drift
failure is operationally investigated rather than silently clamped. Global blob reuse
does not discount the increment or deletion charge.

## IMPLEMENTED PUBLICATION SCHEMA

Migration 000008 introduces:

1. users.used_bytes, backfilled from SUM(blobs.size_bytes) through files. The migration
   excludes concurrent file writers. It fails on overflow or existing over-quota data;
   operators resolve inconsistencies explicitly. Existing files are never discarded.
2. Blob lifecycle state ACTIVE / GC_PENDING and gc_after, with state constraints.
3. Durable candidate/cleanup records identifying an exact opaque physical generation,
   its expected digest/size and state. Final names are random, not SHA-derived paths.
   Cleanup state transitions and worker ownership must be explicit and testable.
4. Narrow runtime grants for intended writes. No runtime DDL or general user-profile
   update privileges. Worker privileges should be scoped separately where practical.

Do not introduce authoritative ref_count. Existing files(blob_id) supports EXISTS
checks. A new candidate cannot silently replace an existing active blob's generation.

## PUBLICATION PROTOCOL

### Prepare without user or blob locks

1. Authenticate and enforce CSRF/Origin and request/rate/capacity limits before costly
   parsing. Complete bounded staging, server hashing and content-policy checks.
2. For the initial local adapter, stage and finalize on one service-owned filesystem.
   Finish writes and sync candidate bytes before taking user/blob locks. The adapter
   must provide a tested durable promotion operation, including parent-directory
   durability where required; atomic rename alone is not crash-durability evidence.
3. Allocate random final-generation keys and commit durable candidate intents before
   any final object can appear. A failure here leaves only temporary staged content.
   No intent or object is exposed as a user-owned file yet.

### Publish in a bounded READ COMMITTED transaction

Acquire locks in this global order for all quota-affecting writers:

1. user row;
2. bound session row, rechecking ownership, enabled account, revocation and expiry;
3. transaction-scoped advisory locks for distinct SHA-256 values, sorted by a stable
   documented lock key (and deduplicated). A truncated lock-key collision only adds
   contention; full SHA-256 equality remains authoritative;
4. blob rows, then candidate-intent rows in deterministic order.

Do not hold these locks during network receipt, hashing or a full object-storage copy.
The initial local adapter's already-staged promotion is the only bounded storage
operation allowed inside this transaction. Lock/statement/transaction deadlines apply.

For each unique digest, re-read the blob after obtaining its digest lock:

Execute the guarded quota increment before any candidate promotion; it remains
uncommitted and rolls back if any later step fails.

- Existing ACTIVE: verify size consistency and generation availability. Reuse the
  existing generation, not the newly allocated candidate. A missing/corrupt generation
  fails closed for repair rather than creating another reference to missing bytes.
- Existing GC_PENDING: uploader and collector use the same digest lock. If the row and
  content remain, atomically revive it before creating references. If the collector
  already removed the generation/row, create a new random generation from staged bytes.
  If the row remains but bytes are missing after a prior collector commit failure,
  recheck NOT EXISTS under the same locks and replace only that unreferenced pending
  generation with a new durable candidate; never revive missing bytes as ACTIVE.
- No blob: lock/check the candidate intent, promote the staged content durably, then
  insert the blob row referencing its exact generation.

After guarded quota admission, insert all owned file rows. Resolve or schedule cleanup
of candidate intents in the same metadata transaction. Return logical file metadata
only after commit. SHA digests, physical keys, reused/new flags and another user's
metadata never appear in upload responses. Do not vary response codes by dedup outcome.

Candidate creation, promotion and intent resolution must never run in an uncoordinated
background task. If PostgreSQL loses the transaction while a filesystem operation
finishes late, no logical publication may proceed; the candidate remains an orphan
eligible for conservative recovery. Ambiguous commits are reconciled by state lookup,
never immediate physical deletion in an error handler.

### Storage deployment boundary

The initial local implementation supports one service-owned durable volume. Multiple
API replicas require access to the same coherent durable volume; PostgreSQL locks do
not share files across independent disks. Unsupported layouts must not be advertised
as supported Kubernetes deployment modes.

An S3-style adapter needs a separately reviewed promotion/durability/recovery protocol
before use. Do not extend a database transaction around an unbounded network Put.
The minimal BlobStore boundary should express immutable Put/Open/Delete or equivalent
semantics without exposing platform paths to domain code.

## CLEANUP AND GC PROTOCOL

Deletion of the final logical reference locks the digest/blob and uses indexed
NOT EXISTS to mark GC_PENDING with gc_after. It does not delete bytes inline.

A worker takes the same digest lock, locks/rechecks the blob and durable cleanup
intent, and rechecks NOT EXISTS. It records DELETING, detaches the unreferenced blob
metadata, and commits BEFORE physical deletion. Only a successful commit authorizes
exact-generation removal. New publication then uses a fresh opaque generation even
if old bytes remain. Holding a DB transaction during unlink is insufficient: losing
that transaction could allow revival while delayed physical deletion still finishes.
Grace time reduces churn; it does not establish correctness. Failed or ambiguous
retirement commits never authorize removal. Durable tombstones permit retries.

An orphan-candidate worker locks the intent and verifies that its key is not referenced
by a committed blob before deleting. Upload publication and cleanup compete on that
same intent state/lock. Cleanup may not remove a candidate that a valid publication
transaction currently owns. Unresolved writer outcomes retain cleanup records/tombstones
for retries/reconciliation; an expired timestamp alone is not sufficient proof that a
writer cannot still finish. The worker commits a DELETING fence before removing an
unreferenced candidate and retains that tombstone for periodic exact-key retries,
including after success, so late I/O is recovered. Bias toward an orphan leak when
ownership is uncertain. See the [lifecycle guide](../architecture/file-lifecycle.md).

Temporary staging cleanup is separate from published-blob GC. A future lease/ownership
scheme must distinguish active uploads from abandoned staging across processes. Do not
implement a naive age-only recursive sweep of a directory containing active uploads.

## FAILURE REVIEW

| Failure / race | Required result |
|---|---|
| Multipart/validation/quota rejection | No logical files or usage charge; temporary cleanup; durable candidates scheduled for cleanup |
| Two same-user uploads near quota | User lock serializes admission; no overshoot |
| Two users upload identical bytes | Digest lock + uniqueness converge on one referenced blob; independent quota charges |
| Multiple equal files in one batch | One referenced blob; one logical charge per file; consistent lock ordering |
| Storage promotion fails | Roll back DB changes; intent survives for cleanup/retry |
| Promotion succeeds, DB rolls back | No visible file; durable orphan intent remains |
| Commit outcome is unknown | Query/reconcile state; never delete potentially referenced content |
| Upload competes with GC | Same digest lock serializes reuse versus removal; deleted generation never reused |
| Cleanup fails or loses DB ownership | Keep durable intent, retry; never delete by hash alone |
| Account disabled/session revoked during receipt | Transactional recheck rejects publication |
| Response lost after successful commit | File exists and quota is charged; retry semantics need idempotency before promising exactly-once upload |

Idempotency keys are not implemented. Until that follow-up exists, a retry after an
ambiguous response can create a second logical file and quota charge. Document this
in the upload client contract; deduplication is not logical-request idempotency.

Timing remains a potential deduplication side channel even with equal response shapes.
Always receive/hash supplied bytes and expose no presence oracle; measure timing before
claiming cross-user timing indistinguishability. Global physical savings belong only in
authorized administration/statistics, not a per-upload reuse flag.

## VALIDATION REQUIRED BEFORE ENABLING UPLOAD

The core is exercised with separate pools, real storage, and TLS/GraphQL requests.
See [implementation and validation](../architecture/upload-publication.md). The full
acceptance list is below. Worker races and injected database/storage failures are tested;
ambiguous network-commit drills and an idempotency API remain follow-ups:

- Migration backfill/rollback and runtime privilege checks.
- Exact quota boundary, configurable quota, duplicate logical charges, zero-byte files,
  overflow rejection, mixed-owner attempts and atomic batch rollback.
- Concurrent same-user quota races and cross-user identical-content races.
- Stored usage versus relational SUM reconciliation under the user lock; report drift
  and repair only through an explicit operator action using the same lock protocol.
- Injected filesystem/DB failure before and after promotion, ambiguous commit recovery,
  GC-versus-upload interleavings and retry after worker interruption.
- No raw paths/digests/reuse hints in user responses and no cross-user file access.

## ACTIONABLE MOMENTUM

File queries and deletion/GC are implemented. Next: sharing or authorized administration.
