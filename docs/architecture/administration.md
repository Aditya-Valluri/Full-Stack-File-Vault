# Administration and audit

## WHAT

Administrators can list users and file metadata, filter files by uploader, inspect
global storage statistics and shared-download counts, change quotas, and enable or
disable USER accounts. Role assignment, provisioning, and administrative-account
recovery remain operator procedures. Admin metadata visibility does not confer
private byte access, ownership, sharing authority, or file-deletion rights.

## WHY & TRADE-OFFS

A transaction-time role check catches demotion after middleware authentication.
Middleware-only authorization is cheaper but can trust stale roles. The service locks
all participating users in UUID order, then rechecks the actor's session and current
ADMIN role, matching sharing's multi-user lock order.

An audit insert commits in the same transaction as every successful mutation; if
audit insertion fails, the change rolls back. The runtime role has SELECT/INSERT only
on audit records. Database administrators remain a separate trust boundary; this is
not cryptographic tamper evidence or an external immutable audit archive.

## THE HOW

Migration 000013 adds admin_audit and narrow runtime grants for quota/status updates.
The internal/admin service centralizes policy and explicit SQL. GraphQL exposes:

~~~graphql
query Administration {
 adminUsers(first: 20) {
  nodes { id loginName role usedBytes quotaBytes disabledAt }
  pageInfo { endCursor hasNextPage }
 }
 adminStorageStats {
  userCount fileCount logicalBytes referencedBytes pendingDeletionBytes
  savedBytes savingsPercent downloadStarts
 }
}
query Files($owner: ID, $after: String) {
 adminFiles(first: 20, after: $after, ownerId: $owner) {
  nodes { ownerId loginName downloadStarts file { id name sizeBytes createdAt } }
  pageInfo { endCursor hasNextPage }
 }
}
mutation Quota($id: ID!, $bytes: String!) {
 adminSetQuota(userId: $id, quotaBytes: $bytes) { id quotaBytes usedBytes }
}
mutation Status($id: ID!, $disabled: Boolean!) {
 adminSetUserDisabled(userId: $id, disabled: $disabled) { id disabledAt }
}
query Audit($after: String) {
 adminAudit(first: 20, after: $after) {
  nodes {
   id actorId targetUserId action occurredAt previousQuota newQuota
   previousDisabledAt newDisabledAt revokedSessions revokedShares
  }
  pageInfo { endCursor hasNextPage }
 }
}
~~~

All operations require the authenticated browser Origin/CSRF boundary and the existing
per-user request budget. Pages are limited to 1..50, and GraphQL complexity bounds apply.
User/file cursors are UUID positions in ascending primary-key order; audit cursors are
decimal identity positions in descending order. They are positions, not permissions or
cross-request snapshots. Inserts concurrent with pagination can require a refresh.

Quota accepts a canonical nonnegative decimal bigint string; values above the 10 MB
default are allowed. It cannot fall below stored logical usage. The existing user lock
serializes quota changes with upload/delete. No automatic data deletion or counter
repair occurs.

Disabling a USER atomically marks it disabled, revokes its sessions, deletes its owned
sharing links (cascading byte grants), and records counts in the audit entry. Files and
quota usage remain. Re-enabling does not resurrect sessions or links. ADMIN accounts
cannot be disabled through this API, preventing self/last-admin lockout. Operators
handle administrative role/status recovery.

Audit records contain actor, target, action, timestamp, before/after quota/status, and
revocation counts. They contain no credentials, content, URLs, or share tokens. Repeated
successful requests are audited even if their requested state was already in effect.
Failed/denied mutations do not create success records. Export/retention/alerting remain
operational work; the down migration refuses to discard nonempty audit history.

Global statistics describe database-referenced content, not measured filesystem usage.
ReferencedBytes deduplicates content across all owners. PendingDeletionBytes describes
GC_PENDING metadata. Staging, detached generations, filesystem overhead, and tombstones
are excluded. Numeric aggregates are returned directly as decimal strings, avoiding
bigint-sum overflow in Go. Shared-download starts retain the sharing guide's admission,
not completion, semantics.

## VALIDATION

PostgreSQL/TLS integration tests cover every operation's ordinary-user denial, stale
ADMIN demotion, bounded pagination/uploader filtering, global dedup deltas, unchanged
private-byte ownership checks, quota lower-bound/above-default behavior, runtime audit
tamper denial, rollback when audit INSERT is denied, admin lockout protection, permanent
session/share revocation, and preservation of logical files.

## ACTIONABLE MOMENTUM

Next in the authorized sequence: React/TypeScript frontend.
