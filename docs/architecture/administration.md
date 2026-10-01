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

## Identity display and uploader selection

Admin user pages, file metadata, and quota/status mutation responses resolve
legacy usernames first. If no legacy credential exists, a matching password
identity and verified account email supply the display name. These lookups use
unique-key joins in the existing page query; no per-user query or password-hash
selection is needed. An account with neither supported identity is labeled
"Account without a login identity" rather than implying that verified email
accounts were never provisioned.

The uploader selector reuses the adminUsers field through a minimal
AdminUploaders operation (id, loginName, cursor metadata). It requests 50 accounts
at a time, below the existing GraphQL complexity limit, with explicit Load more
and Retry actions. Display labels are human-readable; filter values remain UUIDs.
A selection opened from a user row retains its label even before that user's
selector page loads. Account identities remain admin-only; private tags and
file-content authorization are unchanged.

## Resource and security audit

Migration 000022 extends the existing admin audit table, API, and role-protected
feed. Transactional triggers record authenticated-session creation, uploads,
file deletion, share creation/removal/opening, shared download starts, rejected
password attempts, and account security-version changes. Owner downloads are
counted once per short-lived grant after a GET opens the content successfully.
HEAD requests and preview requests do not count as downloads. A download start
does not prove the entire transfer completed.

Authenticated-session creation covers login and registration; it is labelled as
such rather than claiming every new session came from a password login. A share
removal can result from explicit revocation, expiry cleanup, account disabling,
or file deletion. Unknown/system actors are null rather than incorrectly
attributed to the file owner. Anonymous public-share visitors remain anonymous.
Rejected logins contain no submitted identifier, IP address, credential, or hash.
Existing admission budgets bound rejected-login event creation.

File/share UUIDs are snapshots, not live foreign keys, so removing a file does
not remove its audit history. The runtime role still cannot update or delete
audit records. Existing quota fields are meaningful only for administrative
events; resource events use zero placeholders and the UI displays resource IDs
instead. All event writes roll back with failed mutations. The feed sorts the
numeric database ID, not its GraphQL string representation.

Audit history is metadata and needs an operator retention policy. A suggested
starting point is 90 days, adjusted to operational requirements. No automatic
deletion job is introduced here. An authorized database operator can remove
expired records in bounded batches during maintenance; the web runtime must
not receive DELETE privileges. Monitor table size before enabling a sustained
high-volume public deployment. Migration rollback removes only the additional
event kinds to restore the prior schema, so export required audit history first.
