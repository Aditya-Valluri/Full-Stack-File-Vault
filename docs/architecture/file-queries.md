# Owned file listing, search, and metadata

## WHAT

`files` returns the authenticated owner's logical files and `file(id)` returns
one owned file's metadata. Both reuse `VaultFile`; neither exposes hashes, physical
keys, blob IDs, reuse flags, download URLs, or other users' metadata. Admin accounts
have the same owner-only behavior here. Global administration remains a separate step.

## WHY & TRADE-OFFS

Newest-first keyset pagination uses the existing
`files(owner_id, created_at DESC, id DESC)` index. The UUID breaks timestamp ties.
Offset pagination is simpler but can shift under concurrent inserts and requires
the database to compute skipped rows. Keyset pages are not a frozen snapshot: newer
uploads appear on a refreshed first page, while concurrent changes to existing
metadata may affect later results.

Filename matching is a case-insensitive literal substring using PostgreSQL ILIKE.
Percent, underscore, and the chosen escape character are escaped; user input never
becomes SQL syntax or a regular expression. A trigram index could improve large
collections, but adds write/storage cost and requires measured justification.
Substring/filter searches may scan an owner's rows; a 50-row result limit alone
does not bound scanning work. A three-second statement deadline bounds execution.

The reader reuses the user-then-session lock protocol to recheck current account
and session authorization. That serializes a user's reads with uploads, unlike a
cheaper middleware-only identity snapshot. Five-second request and two-second lock
deadlines bound this trade-off. SQL always includes the trusted owner ID.

No new migration or dependency is needed.

## THE HOW

`apps/api/internal/files` owns SQL, input validation, cursors, and metadata results.
Resolvers only translate GraphQL inputs/results. All values are bound SQL parameters;
optional predicate text is fixed server code.

```graphql
query Browse($after: String, $filter: FileFilter) {
  files(first: 20, after: $after, filter: $filter) {
    nodes { id name sizeBytes detectedMIME createdAt }
    pageInfo { endCursor hasNextPage }
  }
}
query Detail($id: ID!) {
  file(id: $id) { id name sizeBytes detectedMIME createdAt }
}
```

Example variables:

```json
{
  "filter": {
    "nameContains": "report",
    "mimeType": "text/plain",
    "minSizeBytes": "0",
    "maxSizeBytes": "1000000",
    "createdFrom": "2026-01-01T00:00:00Z",
    "createdBefore": "2027-01-01T00:00:00Z"
  }
}
```

Filters combine with AND:

- `nameContains`: literal substring, case behavior follows the database locale;
  maximum 255 Unicode characters. Empty string means no filename restriction.
- `mimeType`: exact detected base type, ignoring stored detection parameters.
  Client-declared MIME is not consulted. No wildcard types or parameters are accepted.
- Size bounds: inclusive, nonnegative decimal strings within signed bigint range.
  Zero is supported. Minimum cannot exceed maximum.
- Date interval: inclusive `createdFrom`, exclusive `createdBefore`. Both optional;
  a supplied pair must be increasing.

`first` defaults to 20 and must be 1..50. The reader fetches at most first+1 matching
rows to compute hasNextPage; there is no full-result count. An empty page returns
`nodes: []`, `endCursor: null`, and `hasNextPage: false`.

Pass the returned endCursor unchanged for the next page. Cursors are versioned,
size-bounded base64url positions containing this owner's ID and last file tuple.
They are not secret, signed credentials, or proof of ownership. Foreign-owner
cursors are rejected; forged same-owner positions cannot change the SQL owner
predicate. Reset the cursor when changing filters. Cursor pages do not require the
anchor row to continue existing.

Queries require the established browser cookie, exact Origin, and CSRF token, and
consume the shared user rate allowance. GraphQL complexity now has a 500-point budget
with list cost multiplied by requested page size (clamped to 1..50 for estimation).
This accommodates a full 50-file selection while rejecting aliased expensive lists.

Invalid pagination/filter/cursor input returns INVALID_INPUT. A missing or foreign
file ID produces identical NOT_FOUND errors with `file: null`. Malformed file IDs
also return NOT_FOUND. Anonymous calls return UNAUTHENTICATED; database failures
remain generic. This API accepts no client owner/uploader argument.

## VALIDATION

Unit tests cover filter bounds, literal escaping, cursor parsing, and complexity
rejection. Isolated PostgreSQL/TLS tests cover timestamp ties, a new upload between
pages, combined filters, empty pages, size zero, owner isolation over a shared blob,
admin non-bypass, session revocation, CSRF, safe missing-file errors, and database
failure. The suite runs through the restricted runtime database role.

From `apps/api`:

```powershell
go test ./...
go vet ./...
go build ./...
go test -tags integration ./cmd/provision-user -run TestProvisionCommandIntegration -count=1 -v
```

Production-scale query profiling and frontend/browser UAT remain future work.

References: [PostgreSQL pagination](https://www.postgresql.org/docs/17/queries-limit.html)
and [literal pattern matching](https://www.postgresql.org/docs/17/functions-matching.html).

## ACTIONABLE MOMENTUM

Authorized downloads and deletion/GC are implemented in the [lifecycle guide](file-lifecycle.md).
Next choices: sharing or authorized administration.
