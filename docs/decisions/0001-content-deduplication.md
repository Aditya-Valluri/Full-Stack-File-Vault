# ADR 0001: Separate content from file ownership

Status: Accepted for the Step 1 foundation.

## Context and decision

Store shared content metadata in `vault.blobs` and user-owned references in
`vault.files`. Store bytes outside PostgreSQL under an application-controlled,
opaque storage key. The unique 32-byte SHA-256 digest identifies shared content.
Authentication mapping remains deferred; `vault.users.id` is the stable internal ID.

An alternative is to store bytes and metadata per file in PostgreSQL. That offers
atomic content/metadata commits but increases database and backup size. External
storage supports streaming and object storage but requires failure recovery across
the database and storage backend. Neither upload nor recovery is implemented yet.

## Invariants and future implementation requirements

- Hash received bytes on the server. Client hashes never establish ownership.
- Resolve access through the authenticated user's file record. Do not expose blob
  lookup or download access merely because a caller knows a digest.
- On digest reuse, verify stored size consistency. Treat a mismatch as an error.
- A blob may have multiple references, including multiple references from one user.
- Filenames are display metadata, not physical paths. Storage keys are internal.
- Client-declared MIME belongs to a file; server-detected MIME belongs to content.
  Neither field alone establishes that content is safe to render or execute.
- Restrictive foreign keys prevent deleting referenced blob metadata. Physical
  deletion must also coordinate with concurrent uploads and reference creation.
- Make content durable before publishing a usable file reference. Failed commits
  can leave unreferenced objects; future cleanup must reconcile them safely.
- Avoid a stored reference counter initially; use indexed file references.
- Global deduplication can reveal content existence through timing or responses.
  Future upload behavior must avoid disclosing other users' content presence.

## Current boundary

The schema enforces digest uniqueness, lengths, nonnegative sizes and referential
integrity. It does not implement hashing, authorization, storage, garbage collection,
or a restricted runtime database role. The Compose admin role is for bootstrap and
migrations, not the future API.
