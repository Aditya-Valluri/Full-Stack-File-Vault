# AI-assisted engineering record

This record describes engineering inputs and observable verification, not private
reasoning. AI-generated work remains subject to engineer review. It is an initial
record of this change, not a complete retrospective of the project.

## GraphQL error boundary

**Objective:** Prevent internal errors and request secrets from reaching clients.

**Summarized prompt:** Inspect the existing GraphQL foundation, centralize safe error
handling, preserve useful validation errors, add redaction tests, and validate the
change before proceeding to authentication.

**AI contribution:** Inspected the handler and installed gqlgen source. Identified
that ordinary resolver errors use the default presenter and malformed JSON errors
can include request bodies. Implemented a central presenter, transport codes, and
tests using actual gqlgen execution with deliberately failing resolvers.

**Engineer decision:** User authorized the error-boundary implementation after the
read-only assessment. Final human code review remains pending. The implementation
keeps unexpected resolver messages private and preserves only known pre-execution
protocol messages; no speculative authentication or quota features were added.

**Disposition:** Implemented locally for review; not committed or pushed by this task.

**Verification:** Formatting, uncached Go tests, vet and build passed. Tests cover
ordinary/wrapped failures, forged protocol codes, panic values, malformed JSON with
a secret, response/log redaction, useful validation messages and transport codes.

## Browser authentication design

**Objective / summarized prompt:** Draft and review the session, CSRF and authorization
ADR before writing authentication code, preserving GraphQL-only application operations.

**AI contribution:** Compared first-party identity with OIDC and opaque PostgreSQL
sessions with JWTs. Reviewed current configuration/schema/error handling and OWASP
guidance. Proposed session rotation, a constrained pre-login bootstrap, centralized
CSRF checks and acceptance cases for replay, revocation and cross-user access.

**Engineer decision / disposition:** User authorized a design draft only. ADR 0004
is proposed; identity source and account provisioning remain open for review.

**Verification:** Design compared with existing code and documented constraints.
Documentation whitespace checked. No authentication code, migrations, dependencies,
runtime tests or security-test results were produced for this design-only step.

## Operator credentials micro-step

**Objective / prompt:** Implement the accepted first-party operator-account choice
without advancing into sessions, cookies or GraphQL login.

**AI contribution:** Added migration 3, Argon2id primitives, strict hash-profile parsing,
an atomic provisioning command and a hidden-input PowerShell helper. Used the existing
pgx driver and added golang.org/x/crypto for established password hashing.

**Decision / disposition:** User accepted first-party operator-provisioned accounts.
Implementation remains local for review; no automatic commit/push or default accounts.

**Verification:** Uncached Go tests, vet, build and PowerShell syntax checks passed.
Migration 3 applied and clean state verified. PostgreSQL assertions passed and rolled
back their data. Interactive hidden prompting and full command provisioning remain
unverified end to end; session/CSRF/auth-abuse behavior is not implemented yet.

## Provisioning command end-to-end verification

**Objective / prompt:** Verify account creation, password checking, duplicate rollback,
runtime-role rejection and safe output together before starting session work.

**AI contribution:** Added a build-tagged integration test running the actual compiled
command against an isolated PostgreSQL container with fresh ephemeral credentials.
No development credentials are read. The test owns and cleans up its container/volume.

**Decision / disposition:** User authorized verification only; no session implementation,
commit or push. Existing provisioning behavior was preserved.

**Verification:** Integration test passed in 28.46 seconds, including teardown. Checks
cover both roles, stored password verification, significant whitespace, duplicate
rollback, unchanged existing credentials/role, runtime rejection and output redaction.
Interactive PowerShell prompt behavior remains a separate manual validation item.

## Authenticated session lifecycle

**Objective / prompt:** Implement PostgreSQL-backed creation, lookup, expiry and
revocation, with replica and renewal/revocation race tests; defer browser integration.

**AI contribution:** Added migration 4, a focused pgxpool session store, random bearer
tokens stored as hashes, and an isolated database test using two runtime-role pools.
Lookup acquires a row lock before sampling database time and checking eligibility.

**Decision / disposition:** User authorized this bounded session-storage step. No
HTTP authentication, anonymous bootstrap, atomic login rotation or cookie/CSRF gate
was added. Documentation identifies those remaining integration obligations.

**Verification:** Unit tests, vet and build passed. Isolated integration tests passed,
including an observed blocking race with committed revocation, expiry, disabled users,
bulk revocation, grants and unavailable-store behavior. Migration 4 applied locally
with dirty=false. No commit/push performed.

## Pre-login session rotation

**Objective / prompt:** Add short-lived anonymous sessions and atomic login rotation,
preserving failed-login state and preventing double consumption under concurrency.

**AI contribution:** Added migration 5, dummy credential verification for unknown
users, a bounded hashing gate, user-version/row coordination and atomic consume/insert.
Extended the isolated PostgreSQL fixture with replay, failure injection and lock races.

**Decision / disposition:** User authorized this lifecycle-only micro-step. No cookie,
GraphQL login or browser CSRF middleware was implemented. No commit/push performed.

**Verification:** Initial integration run failed because Docker was stopped; starting
Docker resolved the prerequisite. Retry passed in 8.56 seconds, including rollback,
concurrent consumption and security-change tests. Unit tests, vet and build passed.
HTTP abuse controls and complete browser validation remain outstanding.

## Cookie and Origin/CSRF boundary

**Objective / prompt:** Implement secure cookie helpers and centralized request
protection before exposing GraphQL authentication operations.

**AI contribution:** Compared the current server/session configuration with OWASP
CSRF and MDN cookie guidance. Added exact-origin configuration, cookie helpers,
pre-body request checks, no-touch session lookup, safe failures and server wiring.
Updated the local launcher and query documentation for required Origin headers.

**Decision / disposition:** User authorized this bounded step. No unconditional
bootstrap-header exemption, GraphQL login, migration or new dependency was added.

**Verification:** Unit tests, vet and build passed. Isolated PostgreSQL/TLS HTTP
integration passed (9.10 seconds total) through chi, middleware and gqlgen, including
cross-session token rejection and fail-closed DB errors. PowerShell syntax passed.
No real-browser cookie test, public deployment, commit or push performed.

## GraphQL session bootstrap

**Objective / prompt:** Implement the next bounded step: beginSession with a narrow
CSRF recovery exception, existing-session reuse and anonymous allocation controls.

**AI contribution:** Added a schema-aware operation classifier, resolver capability,
cookie issuance, shared transactional PostgreSQL budget and configuration. Generated
bindings with pinned gqlgen. Added adversarial operation tests and PostgreSQL/TLS
tests covering reuse, identity preservation, cross-pool concurrency and insert rollback.

**Decision / disposition:** PostgreSQL singleton budget avoids replica-local counters
and an additional Redis service, at the cost of global contention and availability
under deliberate budget exhaustion. Default 60 creations per minute is an ADR, not
the specified per-user two-calls-per-second policy. Login/me/logout remain out of scope.

**Verification:** Unit tests, vet and build passed. Isolated PostgreSQL integration
passed in 11.422 seconds. Migration 000006 applied locally. Real-browser tests, expired
session cleanup and deployment abuse protection remain outstanding. No commit or push.

## GraphQL login and authenticated current user

**Objective / prompt:** User authorized the next login micro-step, then explicitly
requested proceeding with next steps while login validation was running. Completed
login and the subsequent authenticated me query; logout remains separate work.

**AI contribution:** Reused transactional session rotation. Added migration 000007
with shared global/peer/identifier attempt budgets and expiry pruning. Connected login
through an anonymous-session/CSRF capability, rejected ambiguous mutation roots, and
redacted protocol errors that could contain passwords. Added RequireUser and me using
the freshly checked session context. Updated schema bindings, tests and documentation.

**Decision / disposition:** Fixed initial login budgets are ADR defaults, separate from
the required strict per-user API limiter. Direct peer IP is authoritative; forwarded
headers are ignored pending trusted-proxy configuration. Current-user reads use the
middleware authorization snapshot; later sensitive writes require transactional checks.
Consulted OWASP Authentication Cheat Sheet for generic errors and identifier throttling.

**Verification:** Login-only PostgreSQL/TLS suite passed in 14.028 seconds. After me,
unit tests and the full isolated PostgreSQL/TLS suite passed (25.880 seconds), including
shared throttles, expiry, rotation rollback, secret redaction, two identities, role
freshness, disabled/revoked sessions and database failure. Migration 000007 applied
locally. Real-browser/load/deployment acceptance remains outstanding. No commit or push.

## GraphQL logout

**Objective / prompt:** User authorized the next micro-step after login/me: implement
logout with server-side revocation and matching cookie deletion.

**AI contribution:** Added an authenticated logout capability and Boolean mutation,
reused SessionStore.Revoke, expanded the single-root operation guard, and wired the
production handler. Added unit and PostgreSQL/TLS tests for request guards, deletion
scope, injected revocation failure, concurrent activity, replay and session isolation.

**Decision / disposition:** Clear cookies only after successful revocation. A replay
after logout receives UNAUTHENTICATED/401 and a clearing cookie from the boundary.
Storage errors retain the credential for retry. Logout affects the current session,
not every device. No schema migration or dependency was needed.

**Verification:** Unit tests passed. Full isolated PostgreSQL/TLS integration passed
in 24.261 seconds, including the existing deterministic renewal/revocation lock test.
Real-browser acceptance remains outstanding. Changes remain uncommitted/unpushed.

## Bounded upload staging

**Objective / prompt:** User authorized proceeding into the file workflow while
retaining the one-micro-step-at-a-time approach. Inspected the schema, existing ADRs,
API transport and the supplied engineering charter before implementation.

**AI contribution:** Added an internal streaming staging primitive with SHA-256,
bounded MIME detection, random confined temporary paths, byte limits, metadata checks,
seekable read access and explicit cleanup. Used standard-library APIs only, checking
Go os.Root and http.DetectContentType documentation. Added real-filesystem tests for
limits, interrupted streams, cleanup, panic recovery and concurrent staging names.

**Decision / disposition:** Chose disk staging over whole-file memory buffering.
No HTTP multipart endpoint or logical-file publication was exposed. Identified the
older derived-usage quota ADR versus the later explicit used_bytes requirement;
recorded the need for a successor decision at the quota/publication step.

**Verification:** API unit suite passed. Focused staging tests passed again after
panic cleanup coverage (1.278 seconds); vet and build passed. Tests ran on Windows;
Unix permission assertions require a Unix run. Database integration was not rerun
because this addition changes no database behavior. No commit or push performed.

## Multipart transport and quota/publication design

**Objective / prompt:** User explicitly authorized both next listed steps: bounded
GraphQL multipart transport/cleanup and transactional quota/file-publication design.

**AI contribution:** Inspected pinned gqlgen multipart source and protocol docs.
Identified its Content-Length-based memory branch and missing project-specific file
and capacity limits. Implemented a stricter gqlgen Transport using existing staging,
real Upload values and the gqlgen executor. Added real-filesystem/executor tests.
Wrote ADR 0005, preserving/superseding ADR 0002's derived-accounting proposal and
specifying guarded used_bytes, lock ordering, immutable generations and cleanup.

**Decision / disposition:** Disk-only multipart staging with explicit constructor
limits and a restricted single-operation mapping contract. Transport remains
unregistered until publication exists. Quota/publication is a reviewed design only;
no migration, counter, final-object storage or GC worker was claimed as implemented.

**Verification:** Focused multipart tests passed (1.994 seconds), then full unit tests,
vet and build passed. Coverage includes false/unknown lengths, mappings, limits,
pre-body security and capacity gates, deadlines, truncation, panic cleanup and handles.
Design reviewed against concurrent quota/dedup/GC and storage/commit failure scenarios;
those publication behaviors still require implementation and integration tests.
No PostgreSQL rerun, production upload enablement, commit or push performed.

## Five-step upload foundation and application integration

**Objective / prompt:** User requested the next five steps after the Full Stack File Vault rename.
Inspected the existing schema, authentication, multipart transport, unfinished
publication service, tests, and engineering charter. Selected five bounded steps:
publication migration, durable local adapter, transactional publication, distributed
user admission, and GraphQL upload/quota integration.

**AI contribution:** Completed validation of migration 000008 and the local adapter;
added real-database concurrency/failure/reconciliation tests; implemented migration
000009 and a bounded PostgreSQL rolling-window limiter; wired uploadFile, uploadFiles,
and quota through the production application handler; regenerated pinned gqlgen
bindings; documented deployment, configuration, recovery, and client contracts.

**Decision / disposition:** Kept GraphQL-only operations, per-logical-file quota,
random immutable storage generations, and user/session/digest lock ordering. Selected
PostgreSQL admission over process-local counters or a new Redis dependency. Byte
counts use decimal strings. No new dependency was introduced. Removed an unused
quota environment example: operator-configured database quotas remain authoritative.
Reviewed gqlgen upload, PostgreSQL locking, and Go rooted-filesystem documentation.

**Verification:** Final unit tests, vet, and builds passed. The complete isolated
PostgreSQL/TLS suite passed in 34.228 seconds, including uploads above the default
quota, concurrent quota/deduplication, pending-generation replacement, authorization
rechecks, post-promotion rollback, shared rate admission, pre-body rejection, and
GraphQL single/batch uploads. Linux storage tests passed in a network-disabled
container, exercising hard-link promotion and directory sync. Local migrations 8
and 9 applied successfully; schema_migrations reports version 9, dirty=false.

**Corrections during validation:** Updated the failing-resolver fixture for the new
quota field and gqlgen Upload pointer types. Corrected the integration fixture to
share one blob volume with its shared database; separate directories correctly
triggered missing-content rejection. The first Linux invocation had an incorrectly
quoted PowerShell test flag; the corrected run passed. A publication-only test
approval was initially declined; subsequent approval covered the completed suite.

**Remaining limits:** No automatic physical GC, crash-orphan cleanup, upload
idempotency, ambiguous-network-commit drill, browser UAT, or deployment/load claim.
Windows development omits directory fsync; Linux filesystem/volume durability still
requires deployment-specific validation. New work and earlier feature changes remain
uncommitted and unpushed.
## Owner-scoped file listing, search, and metadata

**Objective / prompt:** User authorized the next step after uploads/quota/rate limiting.
Selected file listing/search, including an owned-file metadata lookup, without starting
download transport or deletion/GC.

**AI contribution:** Added internal/files with validated SQL filters and keyset cursors;
exposed files and file(id) through gqlgen; wired the reader into the authenticated
application; added safe NOT_FOUND/INVALID_INPUT handling and list-cardinality complexity
weighting. Updated README and the file-query guide. No migration or dependency added.

**Decision / disposition:** Reused the owner/creation/UUID index rather than offsets or
an unmeasured trigram index. Every query includes the session-derived owner; even admin
users receive only their own files. Current authorization is rechecked under the existing
user/session lock protocol. Cursors encode positions, not authorization, and pages are
not a cross-request snapshot. Exact detected MIME, literal filename substring, inclusive
size bounds, and half-open date ranges combine with AND. Total-count aggregation is omitted.

**Verification:** Unit tests, vet, and build passed. Full PostgreSQL/TLS integration
passed in 30.230 seconds. Review found and corrected a legacy NULL-MIME filter inconsistency;
affected files/GraphQL unit tests and the targeted PostgreSQL/TLS suite then passed
(23.916 seconds). Coverage includes timestamp ties, inserts between pages, literal
wildcard characters, combined filters, zero size, cross-user shared blobs, admin
non-bypass, revoked sessions, CSRF, safe missing-ID errors, legacy MIME fallback,
complexity rejection of aliased lists, and unavailable storage. git diff --check passed.

**Remaining limits:** Substring scans need production-scale profiling; statement/page
limits are enforced but do not guarantee index-only filtered searches. No frontend UAT,
download authorization/transport, or deletion/GC was implemented. Changes remain saved
locally, uncommitted and unpushed. Database schema remains at version 9.
## Ten-step file lifecycle increment

**Objective / prompt:** User authorized the next ten steps. Scope: safe object reads,
hashed access grants, GraphQL grant issuance, HTTP byte transport, restricted previews,
logical deletion/quota release, published GC, orphan-intent recovery, a managed worker,
and owner-only storage statistics.

**AI contribution:** Implemented migrations 000010/000011, rooted read/removal operations,
session-bound content authorization, GraphQL lifecycle resolvers and regenerated bindings,
GET/HEAD/single-range transport, transactional logical deletion and statistics, and the
separately privileged cleanup command/launcher. Added lifecycle and cleanup integration
coverage and updated the README, runbook, and ADR 0005.

**Decision / disposition:** Opaque grants expire within 60 seconds, are hashed at rest,
and require their issuing session. Inline previews allow detected PNG/JPEG/WebP only.
Deletion cascades grants and releases logical quota in one transaction. Cleanup commits
a durable DELETING fence and detaches unreferenced blob metadata BEFORE filesystem I/O;
holding database locks through an unlink would be unsafe if the transaction were lost.
Retained tombstones recover late promotions. Statistics reveal no other owner's savings.
Application operations remain GraphQL-only.

**Verification:** Go unit tests, vet, build, and PowerShell syntax checks passed. The full
isolated PostgreSQL/TLS suite passed in 15.847 seconds, including migration up/down rounds,
expiry/caps/session isolation, deletion rollback/quota, protected previews/ranges, narrow
worker privileges, delayed-removal vs new-publication, live-reference protection, held
digest locks, failed retirement, lost removal acknowledgement, late orphan recovery, and
concurrent workers. Linux storage tests passed in a disposable read-only container with
writable tmpfs, exercising file/directory sync and read-after-unlink semantics.

Docker Desktop was initially stopped and was started for validation. Local migrations
10 and 11 applied; schema_migrations reports version 11, dirty=false. First local worker
startup exposed a missing development directory. Added development-only directory creation,
matching the API; worker checks/vet/build then passed and start-gc.ps1 -Once completed with
zero pending records. The generated worker credential is ignored; no secrets were printed.
Removed the temporary local worker build artifact.

**Remaining limits:** Tombstones are retained indefinitely; safe compaction needs a
writer-lifetime proof. Crash-abandoned temporary staging/prepared files need separate lease
recovery. Upload idempotency, power-loss/volume-driver/load drills, sharing/admin/frontend,
and browser UAT remain follow-ups. Already-open streams may complete after revocation.
All feature changes remain saved locally, uncommitted and unpushed.

## Sharing and administration

User authorized the remaining roadmap in order, then confirmed continuation.
Implemented read-only public/recipient sharing, expiry/revocation, anonymous browser
session binding and rate admission, download-start counts, then admin metadata/users,
quota/status controls, global statistics, and transactional append-only audit records.
The sharing suite passed in 50.009 seconds. The full suite including administration
passed in 53.734 seconds; unit tests, vet, and build also passed. Migration 12 was applied
locally. The admin migration is being applied next. Source policy, trade-offs, APIs,
and limits are documented in architecture/sharing.md and architecture/administration.md.
Frontend and the remaining roadmap are still in progress; no commit/push yet.

## Frontend, recovery, deployment, and operations continuation

Implemented the React/TypeScript UI and explicit same-key upload retry behavior selected
by the user. Added immutable upload receipts (migration 14), lease-aware temporary-file
recovery, non-root runtime images, HTTPS rehearsal, CI, Kubernetes templates, and bounded
Prometheus metrics. Four browser scenarios passed, including response-loss retry and
accessibility checks; Linux storage/crash cleanup tests and Go unit/vet checks passed.
Production-mode HTTPS upload/download/delete and secure-cookie checks passed.
Prometheus validated all ten rules and scraped both internal targets successfully.

Added paired database/file backups with authenticated encryption and separate local key
storage. Initial isolated database restoration and tamper-rejection checks passed.
The user selected private encrypted object storage for production; no provider/bucket
credentials or actual off-host destination are configured. Current production boundaries
are documented in architecture/operations.md. Earlier remaining-work notes above describe
historical checkpoints, not the current feature inventory. Final review is still ongoing.
## Private tags and uploader filtering

User selected owner-private tags. Implemented migration 15, bounded normalized
per-file tags, atomic owner-authorized replacement, AND tag filters and literal
uploader-login filtering within owner scope. Shared metadata has no tags;
administrator-wide file projections do not populate them. Added UI editing and
search controls. Validation caught a missing mutation allowlist entry and a test
fixture hash constraint; both were fixed before rollout. Unit/vet, PostgreSQL
integration and all four browser scenarios passed. Applied migration 15 and
updated the local HTTPS containers. The separate Windows PowerShell provisioning
compatibility fix preserves exact UTF-8 password bytes without a BOM or newline.
## Account header correction

User requested replacing the generic account label and shortened UUID. Added the
server-authenticated username to session lookup/creation and GraphQL identity/login
responses, then displayed it with its initial and a labeled account-ID copy action.
No migration or credential change was required. Backend unit/integration checks
and vet passed; all four browser scenarios passed, including identity after reload
and mobile accessibility/layout checks. The local container images were rebuilt.
## Render hiring demo preparation

Added a paid-resource Blueprint, non-root combined demo image, loopback API/worker
supervision, restricted child database credentials, and repeatable reviewer setup.
The official Render JSON schema validation, image build, command tests/vet and
isolated HTTPS browser rehearsal passed. The rehearsal covered non-superuser
migrations, login, uploads, deduplication, sharing, admin, repeat setup and restart
persistence. Initial test harness database-readiness and dynamic-port assumptions
were corrected before the passing run. CI now checks the script and builds the
image. No Render resources have been created; cloud disk permissions, hosted CI,
public HTTPS and managed-database deployment remain to be verified. Cost approval
and account access are required for deployment. Operator credentials remain in
the demo supervisor environment; this is not process-level security isolation.

## Free Render demo-only PostgreSQL BlobStore

User required a $0 Render-only recruiter demo and explicitly constrained database
file storage to a demo adapter when durable storage is impractical. Free Render
cannot attach a volume; another object provider would exceed that hosting scope.
Added demostore.BlobStore selected by BLOB_STORAGE_BACKEND=postgres-demo, retaining
the production contracts and default local adapter. Production's future target
remains durable external object storage, documented in ADR 0006 and README.

Embedded demo-only SQL enforces immutable objects, separate runtime/collector
privileges, a serialized 100 MB/10,000-object cap, and a database-size admission
threshold. Files are limited to 10 MB. Review corrected preparation to copy/hash
before metadata locks, with a 22 MB prepared-payload reservation. Uploads first
stage to private temporary disk; this bytea adapter still needs bounded RAM copies.
The initial integration test used an invalid retry-key fixture; corrected it to
UUIDv4. Unit/vet, full integration (183.131s), subsequent focused adapter integration
(35.848s), image build, free Blueprint guard and workflow lint passed. Final browser
rehearsal used 512 MB/no swap and replaced the whole app container, verified original
owner/shared downloads, duplicate deletion and physical GC; notice visibility and
mobile overflow checks also passed. No live Render resource or public URL is claimed.

The root Blueprint requests only Free services under new names, with startup setup
instead of a paid pre-deploy phase. Docs describe expiration after 30 days, account
usage overages, preservation of existing deployments, and explicit reviewer access.

## Project naming consistency

Standardized the display name as Full Stack File Vault and technical identifiers as full-stack-file-vault across the Go module/imports, frontend, Compose images and projects, Kubernetes, monitoring, Render configuration, and documentation. Removed legacy company branding from current source comments and documentation. Existing Git history is retained.

Validation: Go tests and vet, frontend build and unit tests, free Render configuration guard, and production-mode browser smoke checks passed. Both local Compose projects were recreated using byte-verified copies of their stopped data volumes. Original snapshots are retained in consistently named Docker volumes ending in _rename_backup. The repository checkout directory remains independent of the project name.

## End-to-end audit (2026-09-21)

Requested a comprehensive consistency and functional review. Ran database integration, Linux race checks, browser suites, backup restoration, demo restart rehearsal, dependency scans, and operational validators. Corrected import formatting, regenerated gqlgen identifiers, aligned temporary-demo upload limits, and changed restore readiness to wait for the final PostgreSQL TCP listener. Detailed evidence and remaining limits are in [the audit report](audit-2026-09-21.md).
