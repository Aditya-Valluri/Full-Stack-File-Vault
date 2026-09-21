# ADR 0006: Temporary PostgreSQL BlobStore for a free Render demo

Status: Accepted for the temporary hiring demo only.
Does not supersede ADR 0005's production storage design.

## Context and decision

The user requires a Render-only deployment with no paid resources, and permits
PostgreSQL file contents only when a persistent volume or durable object storage
is impractical for the temporary demo. Render Free cannot attach a persistent
disk; local files are discarded on sleep/restart/redeploy. An object-storage
provider would introduce another account, usage policy and integration outside
the agreed Render-only demo. Render Free PostgreSQL expires 30 days after creation.

For this constrained deployment, select the clearly named
internal/demostore.BlobStore through BLOB_STORAGE_BACKEND=postgres-demo.
The default (unset or local) remains the existing production LocalStore.
Unknown backend values fail closed. PublicationStore, PreparedObject, ContentStore
and the cleanup storage contract keep their existing signatures and guarantees.
The blobstorage.BlobStore interface only composes those existing capabilities.

Production's target remains durable external object storage, such as an
S3-compatible store with immutable generation keys and conditional creation.
That production adapter is not implemented by this change. The current Linux
volume-backed implementation remains available and continues to be tested.

## Options and trade-offs

| Option | Benefits | Costs / suitability |
| --- | --- | --- |
| Private durable filesystem volume | Existing streaming implementation, no database byte amplification | Render requires paid compute/storage; unsuitable for this $0 demo |
| External object storage | Production target; independent capacity/lifecycle and streaming | Another provider/account and storage adapter; outside the Render-only scope |
| Temporary PostgreSQL bytea adapter | Bytes survive loss of the web container without another provider | Shares 1 GB database allowance; bounded in-memory reads, larger backups, database expiration |
| Ephemeral local final files | Simple free deployment | Produces broken file references after sleep/restart; rejected |

## Publication and authorization

Normal migrations do not create demo objects. Demo startup installs embedded,
repeatable SQL into a separate vault_demo schema as the operator, then starts
the API and collector with their respective restricted database credentials.
Schema changes run transactionally under the demo setup advisory lock.

The publisher still commits a durable candidate intent before publishing an
immutable random generation. Byte insertion uses a separate bounded database
pool/transaction, before the normal metadata/quota transaction commits. This is
intentionally the existing publication protocol, NOT a new claim that bytes and
metadata commit atomically. Failed/ambiguous publication can leave an orphan;
durable candidate fencing and exact-key GC remove it.

The runtime may read bytes and execute bounded put_object, but cannot update or
delete bytes or alter the capacity counter. The collector may execute
remove_object, but cannot read file contents or publish them. Definer functions
have fixed search paths, explicit grants, no PUBLIC execution, and schema-qualified
references. Removal requires DELETING state and no live blob reference.
Authorization remains in the existing owner/shared-content handlers; storage
keys are never proof of ownership and do not become a public download API.

Staging remains private, leased and ephemeral. Prepare copies and verifies length
and SHA-256 before publication locks are acquired. An independent 22 MB prepared
memory budget bounds all retained candidates; Close clears and releases the copy.
Promote performs only bounded database I/O. Database checks verify SHA-256 as well. Downloads are seekable for existing
range support and keep at most two full-file buffers per API adapter admitted
until transfer handles close. The storage pool has at most two connections,
separate from the application's metadata pool to avoid pool starvation.

## Resource limits

- 10,000,000 bytes per file in both the demo transport and adapter.
- 11,000,000 bytes per multipart request; two concurrent upload requests.
- Existing 10,000,000-byte per-user logical quota and 2 calls/sec remain enabled.
- 100,000,000 bytes and 10,000 physical generations across the demo, including
  unpublished orphans until they are physically collected.
- New byte insertion stops if pg_database_size reaches 650,000,000 bytes.
- A row-locked capacity counter serializes concurrent admission and deletion.
- Capacity rejection rolls back the whole batch's file references and quota;
  the UI receives DEMO_CAPACITY_REACHED with a temporary-demo explanation.

Payload memory budgets exclude Go/driver allocation overhead; the demo uses bounded concurrency and a soft Go memory target as additional controls.

These are application admission bounds, not a guarantee about the provider's
storage meter. Metadata, receipts, database overhead, dead tuples and WAL have
their own growth. Repeated duplicate references do not add object bytes but do
add metadata. Monitor database usage and keep this a small reviewer demo.
Logical deletion releases a user's quota immediately; physical demo capacity is
released only after reference-safe GC and its normal grace period.

## Deployment and data lifetime

Free startup has no pre-deploy phase or shell, so render-demo performs setup
before accepting traffic on every wake/replacement. It does not reset passwords,
re-enable accounts, wipe data, or migrate disk-backed content implicitly.
Existing disk-backed blob metadata makes setup fail with a fixed explanation;
use the new free-demo resource names and a fresh database.

The UI labels the build as temporary and asks reviewers to use sample files.
A database expiring after 30 days makes the demo unavailable; it is not silently
recreated, upgraded, or kept alive with artificial traffic. Provision a fresh
review environment explicitly when needed. Production paired disk/DB backup
scripts do not constitute a tested backup workflow for this alternative adapter.

Free plans do not guarantee a permanently free bill when a payment method and
usage overages are enabled. The deployment guide describes no-payment-method
suspension behavior and requires the dashboard to show free resources.

## Verification

The integration suite covers duplicate publication and receipt replay, whole-batch
quota rollback, competing admissions at the cap, 10 MB/oversize boundaries,
seekable reads, immutable keys, least privilege, fenced/idempotent deletion and
exact counter accounting. The browser rehearsal replaces the entire app container
with fresh ephemeral staging, checks original owner/shared downloads, and verifies
last-reference collection. Actual Render deployment validation remains a separate
acceptance step; local Docker tests do not prove cloud account privileges or billing.

References:
- [Render Free limitations](https://render.com/docs/free)
- [Render deployment phases](https://render.com/docs/deploys)
- [Production publication ADR](0005-quota-and-publication.md)
