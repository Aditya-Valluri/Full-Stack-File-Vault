# Private tags and uploader filtering

Tags belong to a logical owned file, not its shared content blob. Identical bytes
uploaded by another user or under another file ID have independent tags. Public
and recipient sharing expose no tags. Administrator-wide file metadata deliberately
returns an empty tag list; elevated application roles do not grant tag-edit access
to another owner's files. Database operators still have database-level access.

Migration 15 creates `vault.file_tags`, with a cascading file foreign key, unique
(file_id, tag) key and (tag, file_id) search index. Runtime privileges are limited
to SELECT/INSERT/DELETE. Rollback refuses to discard nonempty tag data.

`setFileTags(fileId: ID!, tags: [String!]!): [String!]!` atomically replaces tags.
Existing fresh user/session locking serializes edits and deletion. Missing and
foreign IDs return the same NOT_FOUND error. The existing CSRF, mutation-shape
and per-user rate limits apply. Concurrent valid replacements use last-committed
replacement semantics; there is no revision-merge UI.

A file accepts at most 20 input tags, normalized by trimming and lowercasing,
deduplicated and sorted. Each normalized tag is 1–32 ASCII characters, beginning
with a letter or digit; subsequent characters may also include spaces, hyphens
and underscores. Blank tags are invalid. An empty list removes all tags.

`FileFilter.tagsAll` requires every supplied tag (AND semantics).
`FileFilter.uploaderNameContains` matches the uploader login as a case-insensitive
literal substring. Percent/underscore are escaped, not treated as wildcards.
All filters combine with the existing filename/MIME/size/date conditions, while
every list query retains its authenticated-owner predicate. This filter does not
turn My files into a global user or file directory. Since My files contains only
the current user's uploads, a different uploader name yields no results.

In the UI, use the **Tags** action on a file, then **Filters** to combine tags and
uploader username with the existing filters. Commas separate tags; clearing the
editor clears the file's tags. Search clear resets all fields.

Validation covers normalization, deduplicated-copy isolation, foreign edit/read
rejection, all-tag matching, combined literal uploader filtering, atomic rejection,
tag clearing, deletion cascade, GraphQL authentication and CSRF, plus the actual
browser edit/filter/share workflow. Production acceptance evidence is tracked in
`docs/acceptance.md`.
