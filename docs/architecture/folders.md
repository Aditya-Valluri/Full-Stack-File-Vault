# Private folder organization

Migration 000025 adds owner-scoped folders with an immutable parent reference and
a nullable folder reference on each logical file. Existing files stay at root.
Composite foreign keys prevent another owner's folder from being referenced even
if an application check regresses. Folder names are display metadata, never paths
in physical storage. Names are trimmed, case-sensitive, at most 100 Unicode
characters; control characters, path separators, dot and dot-dot are rejected.

The GraphQL API provides folders, createFolder, renameFolder, deleteFolder and
moveFile. The existing user/session lock order serializes changes with publication
and deletion. Empty-folder deletion is enforced by foreign keys. There are at most
1000 folders per owner and 20 levels. The bounded complete tree supports a single
query and hash-indexed browser path lookup without one query per ancestor.
Parent relationships cannot be changed, so ordinary API calls cannot create cycles.

Files can move between an owned folder and root. No bytes are copied, ownership
changed, share revoked, or quota recalculated. Uploads publish at root; moving them
is a separate explicit operation so upload receipt semantics remain unchanged.
Tags remain private per logical file. File queries combine folderId or rootOnly
with existing filters; omitting both searches across all folders with the existing
keyset pagination. Folder selection in the UI makes that scope explicit.

Rollback removes organization only; logical files and content remain at root.
Integration tests cover cross-owner access and mutation denial, invalid parents,
duplicate names, nonempty deletion, depth limits, metadata preservation, filtering
and unchanged quota. Browser coverage includes nested browsing, moves, rename,
empty deletion, details and a mobile viewport.
