# Bounded GraphQL multipart transport

## WHAT

internal/graph/multipart.go implements gqlgen's graphql.Transport interface for a
restricted GraphQL multipart protocol. It produces real graphql.Upload values and
uses gqlgen's executor. NewApplicationHandler registers it with the production schema,
behind browser security and shared per-user admission. See the
[publication guide](upload-publication.md) for the working upload/quota contract.

## WHY & TRADE-OFFS

Inspection of pinned gqlgen v0.17.95 found its MultipartForm transport selects
in-memory buffering using request ContentLength < MaxMemory. Unknown-length requests
can therefore use that branch. It also lacks our per-file/file-count/capacity policies,
uses the process temporary directory, and ignores the error from temporary-file removal.

Using the default transport would reduce maintained code but require separate gates
and spool controls. A custom gqlgen-compatible transport lets us stage once through
the existing bounded hasher in a private directory, including chunked requests, with
explicit cleanup reporting. The cost is maintaining/testing a deliberately narrower
protocol subset. It remains GraphQL multipart using gqlgen Upload and its executor,
not a parallel REST upload route or Base64 JSON encoding.

## THE HOW

NewMultipartTransport requires explicit MultipartConfig values:

| Setting | Meaning |
|---|---|
| Directory | Existing service-owned staging directory |
| MaxRequestBytes | Entire body budget, including metadata, boundaries and epilogue |
| MaxFileBytes | Bytes allowed per physical file part, independent of user quota |
| MaxFiles | Maximum files per operation, supported range 1..100 |
| MaxConcurrentRequests | Active requests per transport instance, range 1..128 |
| Timeout | Parse plus synchronous execution deadline, positive and at most 5 minutes |

The constructor supplies no implicit defaults. The application configuration provides
explicit values documented in the publication guide, including multipart overhead.

Raw file buffering in memory is disabled: every file is streamed to staging. The copy
buffer is 32 KiB. Operations JSON is capped at 64 KiB and map JSON at 16 KiB. JSON data
structures, query parsing and the standard MIME parser add memory overhead; this is not
a claim that total request memory is 32 KiB. Standard MIME header parsing has its own
limits, with input also constrained by MaxRequestBytes. Deploy HTTP header and connection
limits as well. Successful live staging per instance is bounded by admitted requests
times the request-byte budget; this does not bound old crash orphans or other processes.

Request order:

1. Existing browser middleware validates method, exact Origin, Fetch Metadata, cookie
   and CSRF before any body parsing. Bootstrap multipart requests remain rejected.
2. Shared per-user admission runs, then transport independently requires authenticated context. It rejects known oversize
   bodies and capacity exhaustion before reading. Anonymous state does not qualify.
3. Wrap the body with MaxBytesReader regardless of Content-Length. A deadline callback
   closes the HTTP body to unblock reads when cancelled.
4. Read bounded operations then map fields in protocol order. Validate the query
   against the supplied schema before accepting file data.
5. Stage each declared file part, build graphql.Upload and insert it into its one
   validated null variable placeholder using gqlgen RawParams.AddUpload.
6. Reject missing, extra or duplicate parts. Account for any trailing epilogue bytes
   before executing. Invoke gqlgen validation/coercion and synchronous execution.
7. Close/remove every staged file on success, malformed input, executor failure or
   panic unwinding. Release the capacity slot only after cleanup runs.

Cleanup errors are logged with fixed text and a safe code, not temporary paths. Cleanup
failure after a successful business commit must not falsely report that the business
operation rolled back. The eventual publication/recovery service must retain durable
cleanup intent for final objects. Request-local cleanup does not replace crash recovery.

### Supported request contract

Exactly one selected mutation/root, optionally aliased or reached through fragments:

```graphql
mutation Single($file: Upload!) { uploadFile(file: $file) { id } }
mutation Batch($files: [Upload!]!) { uploadFiles(files: $files) { id } }
```

These are current application schema fields.
Single upload maps one file part to variables.<name>; batch upload maps one part per
null element to variables.<name>.<index>. Every placeholder must have exactly one
part. Client filenames remain metadata, never storage paths. Do not strip a supplied
path and silently accept it. Content-Transfer-Encoding is rejected so hashed bytes
are the raw received part bytes.

Batch HTTP operations, multiple roots, directives on root selections, map fan-out,
repeated paths, nested upload inputs, non-null placeholders, unused mappings and
operation extensions are deliberately unsupported. Identical bytes in distinct file
parts are valid; publication deduplicates physical content while charging each file.

Multipart must not execute login/logout/bootstrap, queries or mixed business mutations.
The application handler routes multipart separately from the 64 KiB JSON body limit
and rejects mixed/repeated mutation roots before any resolver executes.

### Safe failures

- 401 UNAUTHENTICATED: absent/anonymous authenticated context.
- 400 INVALID_INPUT: malformed protocol, mapping, metadata or unsupported operation.
- 413 REQUEST_TOO_LARGE / UPLOAD_TOO_LARGE: request/metadata/file limit exceeded.
- 408 REQUEST_TIMEOUT: cancellation/deadline during receipt.
- 503 UPLOAD_BUSY: process capacity occupied; no request body read.
- 503 INTERNAL_ERROR: staging storage unavailable.
- 422 GRAPHQL_VALIDATION_FAILED: executor validation failure, with a fixed message.

Resolver errors use the configured gqlgen error presenter. Configure the existing safe
presenter and panic handling when registering the transport. No success response may
expose hashes, physical paths or another user's deduplication state.

## VALIDATION

Tests use gqlgen's real handler/executor with an executable test schema and the actual
browser middleware/staging filesystem. They verify single/multiple files, aliases and
fragments, unknown and false Content-Length, per-file/request/metadata limits, epilogue
limits, mapping rejection, pre-body auth/CSRF/bootstrap/capacity rejection, cancellation,
truncation, executor panic cleanup and closed handles after execution.
The full unit suite and PostgreSQL/TLS integration cover the production schema and
publication service. Browser UAT, automated crash recovery, and deployment capacity
validation remain outstanding.

References: [gqlgen upload documentation](https://gqlgen.com/reference/file-upload/) and
[GraphQL multipart request specification](https://github.com/jaydenseric/graphql-multipart-request-spec).
