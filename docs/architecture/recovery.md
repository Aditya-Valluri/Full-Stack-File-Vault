# Upload recovery and retry protection

## Idempotent publication

GraphQL uploadFile and uploadFiles accept an optional idempotencyKey (canonical
lowercase UUIDv4). Existing API clients may omit it; those requests retain their
original create-on-every-call behavior. The browser always supplies a key.

The owner and key identify an immutable upload receipt. A versioned fingerprint
binds ordered content hashes, sizes, names, declared MIME, and detected MIME.
The existing user row lock serializes receipt lookup, quota charging, and receipt
creation across sessions and API replicas. The receipt, files, and quota commit
together. A receipt insertion failure rolls back the entire logical publication.

The same owner/key/content returns the original metadata without charging again.
Changed content or order returns UPLOAD_RETRY_CONFLICT. Another owner has a
separate key space. A replay after deletion returns the original receipt; it
never recreates deleted files or restores their access. Normal authorization
still applies to subsequent queries and downloads.

Receipts are retained indefinitely. Runtime privileges allow SELECT and INSERT
only. The down migration refuses a nonempty receipt table. Monitor receipt-table
growth; do not silently expire receipts and turn old retries into new uploads.

The browser offers an explicit Retry upload safely button after an uncertain
response, keeping both the selected File objects and key in memory. It does not
automatically retry. Closing/reopening the upload dialog preserves the pending
attempt; a page reload does not. After a reload, inspect the vault before starting
a new upload. Raw bytes, credentials, and share links are not persisted in browser
storage. Retrying still consumes request admission and bounded staging work.

## Abandoned temporary files

Stage and Prepare acquire nonblocking OS leases held until their temporary handles
are closed. The separately privileged collector scans bounded directory pages,
retaining its cursor between cycles. It only considers exact upload-<64hex>.part
and .candidate-<64hex> names older than one hour.

Before unlinking, the collector acquires the same exclusive lease and rechecks
file identity, regular-file type, and age. Live uploads remain protected even when
older than the grace period. Kernel-released locks after process exit allow crash
recovery. Temporary candidate unlinking never unlinks its final blob hard link.
Symlinks, unknown names, and blob- generations are excluded from this sweep.

Production uses Linux flock and a local filesystem with hard links and directory
fsync. Windows development uses a reserved byte-range lock beyond supported file
sizes. Other operating systems cannot run this cleaner. Network filesystem lock
and durability semantics have not been qualified.

The collector now requires UPLOAD_STAGING_DIR as well as BLOB_STORAGE_DIR in
production. Development defaults staging to data/staging, matching the API.
Both processes must resolve these settings to the same private directories.
One worker cycle inspects at most batch*10 entries per directory.

## Cleanup-record retention

Final-generation DELETING tombstones remain retained and periodically retried.
Age alone cannot prove that a stalled writer will never complete a late filesystem
operation. Automatically deleting those records would weaken recovery guarantees.

Any future offline compaction must first stop every API publisher and cleanup
worker, prove their processes are terminated, remove exact unreferenced generation
keys, and durably synchronize storage before discarding tombstones. This release
does not provide an online age-based compactor. Monitor database growth and
cleanup failures, and include tombstones and receipts in backups.

## Evidence

Unit tests cover fingerprint order/content binding, active-file preservation,
final-generation preservation, age/name filtering, scan progress, and cleanup
after an actual helper process exits without closing its temporary files.
PostgreSQL integration tests exercise concurrent same-key publication across
pools, owner isolation, changed content, deleted-file replay, quota/file rollback
on receipt failure, runtime privileges, and guarded migration rollback.
A browser test lets an upload commit, deliberately drops its response, and retries
the same selection to check that only one logical file and quota charge remain.
