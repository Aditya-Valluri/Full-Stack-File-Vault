# Read-only sharing

## WHAT

Sharing is an explicit grant on a logical file. Files remain private unless the owner
creates a link. Public means anyone possessing that link, not an indexed public directory.
An optional recipient ID additionally restricts redemption to that authenticated account.
Shares never transfer ownership, charge recipients quota, or grant modification rights.

## WHY & TRADE-OFFS

Public bearer links work without an account but can be forwarded. Account-restricted
links require both possession and the specified account. Both use random 256-bit tokens
whose SHA-256 digests alone are stored. A stateless signed URL would avoid a DB lookup
but complicate immediate revocation, owner disablement, and file deletion checks.

DOWNLOAD permits attachment transport only. PREVIEW_AND_DOWNLOAD also permits the
existing PNG/JPEG/WebP inline policy. A preview-only permission would falsely imply
that a browser receiving bytes cannot save them, so it is deliberately absent.

## THE HOW

Migration 000012 adds file_shares, shared_access, shared_request_windows, and
file_download_counts. The internal/sharing service owns authorization and SQL. The
GraphQL layer returns safe models; the byte transport reuses restrictive headers.

~~~graphql
mutation Create($input: CreateShareInput!) {
 createShare(input: $input) { url share { id permission recipientId expiresAt } }
}
query Manage($id: ID!) {
 fileShares(fileId: $id) { downloadStarts shares { id permission recipientId expiresAt } }
}
query Inspect($token: String!) {
 sharedFile(token: $token) { name sizeBytes detectedMIME previewAllowed expiresAt }
}
mutation Access($token: String!) {
 createSharedAccess(token: $token, mode: DOWNLOAD) { url expiresAt }
}
mutation Revoke($id: ID!) { revokeShare(id: $id) }
~~~

CreateShareInput requires fileId and expiresInSeconds (60..2592000); permission defaults
to DOWNLOAD. Omit recipientId for a public bearer link. The URL is returned once as
/share#<token>; the frontend will consume the fragment and remove it from history.
The sharing page itself belongs to the frontend increment and is not implemented here.
Do not put the token in a query string, telemetry, or logs. A lost creation response
requires owner inspection/revocation and a new link; raw tokens cannot be recovered.

Public visitors first call the existing beginSession bootstrap, then use its anonymous
HttpOnly cookie and CSRF token for GraphQL. No account is required. Shared byte grants
last at most 60 seconds and cannot outlive the share or issuing session. GET/HEAD
/shared-content/{grant} requires that exact browser session. It never accepts the
long-lived share token directly. Login rotates the anonymous session; obtain a new
grant after login.

Authenticated requests retain the global configured per-user budget. Anonymous
sharing service calls and byte opens share a PostgreSQL-backed allowance of two per
rolling second per browser session. Invalid tokens consume it too. Bootstrap already
has a global allocation budget. Clients must pace inspection, grant creation, and
download initiation; the third immediate anonymous operation is rate limited.
Edge-wide abuse/bandwidth limits remain part of deployment hardening.

At most 20 links exist per logical file; expired links are pruned when creating another.
Revocation deletes the link and cascades all issued byte grants. Owner deletion also
cascades links/grants. Expiry, owner disablement, recipient identity, session revocation,
and current file availability are rechecked at access time. There are at most 64 active
shared grants per session; expiry pruning also runs in the cleanup worker.

Shared operations lock participating users in UUID order, then the browser session,
then the share. This avoids owner/recipient lock inversion. The file handle opens
under those locks, and the transaction ends before streaming. Already-admitted streams
may finish after revocation/deletion; later opens fail.

DownloadStarts counts admitted DOWNLOAD GET opens once per short-lived grant, including
range retries only once. HEAD and PREVIEW do not increment it. This is not proof of a
completed transfer: a disconnect or invalid downstream range can occur after admission.
Counts survive link revocation and expire with deletion of the logical file.
Owner-only listing never returns stored token digests, raw tokens, or physical identities.

## VALIDATION

Real PostgreSQL/TLS coverage exercises public and recipient-bound links, owner-only
management, sharing without ownership, quota invariance, expiry, disabled owners, deleted
sources, session binding, CSRF, anonymous admission across pools, capacity races,
preview restrictions, download-start accounting, and reciprocal sharing lock order.
A gated storage open verifies deletion waits for admission and blocks future opens.

## ACTIONABLE MOMENTUM

Next in the authorized sequence: role-protected administration and audit records.
