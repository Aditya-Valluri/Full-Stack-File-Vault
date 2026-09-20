# Bounded upload staging

## WHAT

The first file-workflow micro-step adds internal/upload.Stage: stream an io.Reader
to private temporary storage, hash received bytes, detect their MIME type and expose
a seekable read-only staging handle. No logical file or blob is published yet.

SPEC (user-supplied requirement summary): bounded uploads, SHA-256 content identity,
untrusted client MIME/filenames, cleanup and separate logical files/physical blobs.
ADR: local disk staging and the interface below. The original PDF remains unverified.

## WHY & TRADE-OFFS

Disk staging bounds memory and allows the short quota/deduplication transaction to
operate on completed content. In-memory buffering avoids disk IO but memory grows
with upload sizes and concurrent requests. Direct object-storage uploads can support
larger content but introduce transport/verification complexity; gqlgen multipart is
the accepted application upload contract for this project.

The primitive uses a fixed 32 KiB copy buffer and a 512-byte detection prefix. The
caller supplies a positive file-byte limit; it is independent of a user's configurable
logical quota. A one-byte probe detects oversized streams without trusting a claimed
length. At most maxBytes are written and maxBytes+1 bytes consumed. Temporary content
is not fsynced or promoted: successful staging is not durable publication.

## THE HOW

Files: apps/api/internal/upload/staging.go and staging_test.go.

Stage requires an existing service-owned staging directory. Configure restrictive
permissions/ACLs outside the library; do not share it with untrusted local writers.
It opens the directory with os.OpenRoot and exclusively creates a random 256-bit
temporary filename. File creation requests mode 0600 on Unix; Windows protection
depends on directory ACLs. No client filename participates in a filesystem operation.

```go
staged, err := upload.Stage(ctx, stagingDirectory, source,
    upload.Metadata{Name: displayName, DeclaredMIME: declaredMIME}, maxFileBytes)
if err != nil {
    return err
}
// Use staged.Info() and its Read/Seek methods in the future publication service.
// Always check staged.Close() on every exit; it removes the temporary content.
```

The future caller must defer checked cleanup immediately after success, including
when content policy, quota or DB publication rejects the file. Close is idempotent
after removal. Removal errors are reported and can be retried; cleanup never performs
recursive deletion. Failed Stage closes/removes partial files, including during a
source panic. If filesystem failure prevents removal, ErrCleanup is joined to the
original failure and an orphan may remain for operational recovery.

Display filenames must be nonblank valid UTF-8, at most 255 runes, and contain no
slashes, backslashes or control characters. Declared MIME is syntax-checked, bounded
to 256 bytes and stored as a canonical media type without parameters. It remains
untrusted. Server detection uses actual bytes and preserves the detected MIME value.
An apparent mismatch is retained for future format-specific policy, not silently
accepted as proof of a format or rejected using an unreliable universal equality rule.
Empty files are supported and hash consistently. This is content identification,
not complete format validation, malware scanning or permission to render inline.

The returned Info includes a digest for internal deduplication only. Never expose it,
temporary paths or another user's deduplication result through the application API.
Source ownership stays with the caller; Stage does not close source readers.

## VALIDATION AND LIMITS

Tests cover streaming hash/size accuracy, rewind, MIME spoofing, exact/over/empty size
limits, malformed metadata, cancellation, partial reads with errors/EOF, stalled and
invalid readers, concurrent names, repeated cleanup, cleanup failure recovery and panic
cleanup. File-permission assertions run on Unix; Windows tests cover local file behavior.
The API unit suite passed. After adding panic cleanup, focused staging tests passed
in 1.278 seconds; go vet and build passed. No database behavior changed, so the prior
PostgreSQL integration suite was not rerun for this isolated package addition.

Cancellation checks run between reads; an arbitrary blocked io.Reader cannot be
interrupted by context alone. The HTTP integration must set request deadlines and
close transport-owned readers on cancellation. This primitive does not cap total
concurrent staging, multipart spool space, or disk occupancy across processes.

Production GraphQL still rejects multipart. A bounded transport has since been
implemented and tested separately; see [GraphQL multipart](graphql-multipart.md).
Before enabling it, connect aggregate request and
file-count limits, authentication/CSRF before parsing, transport temporary-file cleanup,
capacity admission and safe publication with transactional quota enforcement. Crash
orphan recovery requires an ownership/lease design; do not sweep files by age alone
while another process may be using them. Local staging is not shared durable storage
for independent Kubernetes replicas.

## FOLLOW-UP DECISION

ADR 0002 originally proposes derived quota usage. The user's later architecture
explicitly requires transactionally maintained users.used_bytes plus reconciliation.
The successor [ADR 0005](../decisions/0005-quota-and-publication.md) now specifies
transactional quota and publication. Its counter, migration and enforcement are
not implemented yet; the original ADR history is preserved.

## ACTIONABLE MOMENTUM

Next bounded choice: multipart transport limits/cleanup, or quota/publication design
before making any upload publicly callable.

References: [Go os.Root](https://pkg.go.dev/os#Root) for filesystem confinement and
[http.DetectContentType](https://pkg.go.dev/net/http#DetectContentType) for bounded sniffing.
