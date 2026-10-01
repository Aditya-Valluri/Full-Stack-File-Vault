# Assignment acceptance and UAT

Evidence recorded against the supplied hiring-task PDF, pages 1–6.
Document requirements inform this checklist; operational instructions come from
the user's requests. PASS means tested locally, not production certification.

| Requirement | Status | Evidence / remaining work |
| --- | --- | --- |
| Go, PostgreSQL, GraphQL-only application API | PASS | Typed SDL, generated gqlgen layer, restricted DB roles |
| SHA-256 content deduplication and separate owned references | PASS | Concurrent publication and lifecycle integration tests |
| Single/batch and drag-and-drop uploads | PASS | React dropzone and browser multipart scenarios |
| Declared/content MIME validation | PASS | Upload staging and multipart validation tests |
| Owner listing, metadata, deletion, reference-safe cleanup | PASS | File query/lifecycle integration and browser tests |
| Public and recipient-restricted sharing, expiry/revocation | PASS | Sharing integration; browser guest download and revocation |
| Download counts | PASS | Counts admitted download starts, once per issued grant; not completed transfers |
| Combined filename/MIME/size/date search | PASS | Bounded filters and pagination |
| Tags and uploader-name search | PASS | Private per-file tags and combined uploader login substring filtering, within owner scope; integration and browser coverage |
| Two calls/sec shared across replicas | PASS | Twelve simultaneous admissions across two pools yield two accepted and ten limited; other-user isolation and fail-closed checks |
| Configurable 10,000,000-byte logical quota | PASS | Atomic quota and duplicate logical charging tests |
| Per-user unique/logical/saved storage and percentage | PASS | Browser duplicate uploads display 50% savings |
| Admin files/uploader details, usage, quota, role protection | PASS | Admin integration and browser workflows |
| Responsive frontend and accessibility | PASS | Eighteen Chromium scenarios, axe checks and mobile overflow checks; not exhaustive accessibility certification |
| Authenticated short-lived byte transport and image preview | PASS | Owner/shared grants, browser image preview and download checks |
| Upload response-loss retry | PASS | Explicit same-key retry returns original result with one quota charge |
| Crash-abandoned temporary cleanup | PASS | Lease and helper-process crash tests, including Linux |
| Docker API/web/database and HTTPS rehearsal | PASS | Production-mode cookie/CSP/upload/download/delete smoke |
| CI configuration | PASS | Hosted workflow passed for dab56bf, including race tests, browser checks, images and manifest rendering |
| Kubernetes configuration | PARTIAL | Seven base resources render and pass strict schemas; external PostgreSQL chosen; no cluster rollout |
| Monitoring | PARTIAL | Ten rules validate, both local targets scrape; production receiver and delivery test pending |
| Encrypted backups and recovery | PARTIAL | Real-file paired backup, tamper rejection, isolated DB restore, quota and referenced-blob SHA-256 checks pass; off-host upload and restored-browser drill pending |
| Full load, power-loss, volume-driver qualification | PARTIAL | Race/concurrency tests exist; sustained throughput/latency and infrastructure failure drills pending |
| Public cloud URL | PARTIAL | Earlier public page/readiness checks recorded below; latest authenticated live workflows remain unverified |
| Private folders | PASS | Owner-scoped constraints, bounded hierarchy, integration and browser tests |
| Real-time updates, admin graphs, Helm | MISSING | Optional features; not implemented |
| Activity audit | PASS | Transactional administration and resource/security events; external archival and retention remain operational work |
| Optional TOTP MFA | PASS | Enrollment, replay protection, one-use recovery, session revocation and reset preservation; deployment key required |
| PDF/text previews and view-only shares | PASS | Bounded rendering and server-side download denial; preview bytes can still be copied |
| Documentation and AI methodology | PASS | README, architecture/decision notes, GraphQL SDL, AI work record |

## Automated UAT already exercised

- Sign in/out; production Secure, HttpOnly, host-only cookie and CSP.
- Single image preview; duplicate multi-upload; quota and storage savings.
- Filename search, owned download, delete and quota release.
- Public sharing, guest download, link revocation.
- Admin quota changes, user disable/enable and audit visibility.
- Lost committed upload response followed by explicit same-key retry.
- Mobile/desktop accessibility and layout checks.
- Encrypted paired backup with an uploaded file; authenticated restoration,
  deliberate ciphertext tampering, quota checks and SHA-256 comparison.

Playwright exercises real browser behavior; Go/PostgreSQL integration tests cover
transactional and authorization races that are difficult to schedule from a UI.
axe provides automated coverage, complemented by keyboard/focus assertions.
Neither replaces manual screen-reader review or sustained production-like load tests.

## Remaining release acceptance

Private tag and uploader filters now cover the identified search gaps within owner-scoped listings.
Run the browser against a fully restored deployment, including sharing and replay
receipts. Configure private encrypted off-host storage and verify retrieval under
the restore identity. Measure RPO/RTO and sustained load on the selected storage
driver. Configure TLS/hostname, production database, secrets, monitoring receiver,
and persistent storage; execute a staging rollout and observe the hosted CI run.

## Latest local results

- Complete PostgreSQL integration fixture: PASS, 73.278 seconds.
- Nonempty backup plus HTTPS smoke: PASS, one uploaded file restored and verified.
- Prometheus alert rules: PASS, ten rules.
- Prometheus targets: API and collector both UP.
- Kubernetes strict schema validation: seven valid, zero errors or skipped resources.
- GitHub Actions lint: PASS.

- Frontend dependency audit: zero vulnerabilities.
- Go vulnerability scan (Linux, Go 1.27.1): zero reachable or imported-package vulnerabilities. The required x/crypto module carries advisory GO-2026-5932 for unused openpgp; this application does not import that package.


Private-tags validation: Go unit tests and vet passed; full PostgreSQL integration passed in 25.332 seconds; all four browser scenarios passed in 1.6 minutes, including private tag editing, combined filters, deduplicated-copy isolation and shared-page privacy. Migration 15 is additive; rollback refuses to discard stored tags.

## Free Render demo validation

The demo-only PostgreSQL BlobStore is selected explicitly; local storage remains
production's default and external object storage remains the documented target.
Go unit tests and vet passed. The complete PostgreSQL integration suite passed in
183.131 seconds; after moving preparation before publication locks, the focused
migration/demo suite passed in 35.848 seconds. Prepared-memory limit/release and
production-default selection tests passed. The final image built successfully.
The browser rehearsal passed with 512 MB memory and swap disabled: bootstrap,
login, deduplication, shared/owner downloads after full container replacement,
admin listing, duplicate deletion, GC/capacity release, and 1024/390px notice/layout
checks. The free-resource Blueprint guard and workflow lint passed.
Actual Render deployment, managed database privileges, public URL and account
billing settings remain unverified; no new cloud resources were created here.

## Repository audit (2026-09-21)

See [the audit report](audit-2026-09-21.md) for reproduced inconsistencies, fixes, test results, hosted CI status, and the remaining deployment/recovery gaps. Earlier PASS entries describe their recorded local checks; they are not a claim that the current pushed revision passed hosted CI.

## Release validation update (2026-09-28)

- Hosted GitHub Actions passed for authentication commit 7c745e8 and latest
  pushed commit 8af78c3. Latest run:
  https://github.com/Aditya-Valluri/Full-Stack-File-Vault/actions/runs/36371269143
- The updated Render container built successfully. The isolated rehearsal result
  recorded at 2026-09-28T03:00:07.189Z confirms non-superuser migrations, repeat
  setup, Secure/HttpOnly cookies, content persistence after container replacement,
  shared downloads, administration, upload limits, duplicate deletion, and physical
  capacity release. No live Render resources were changed.
- One read-only check of the existing public readiness endpoint returned HTTP 200.
  This does not establish its deployed commit or validate the public user flows.
- Remaining public-release gates: confirm live revision and Gmail configuration,
  approve any manual deployment, and complete controlled live account/mail/file
  checks. Blueprint resource association remains unresolved; do not approve
  replacement resource creation. See release-checklist.md.
- Production off-host recovery, alert delivery, sustained load, and cluster rollout
  remain unverified. Google sign-in remains deferred.

## Public release smoke checks (2026-09-28)

The supplied Render dashboard identifies 8af78c3 as Live on existing service
srv-daolnujbc2fs73fv5a20. Read-only checks returned HTTP 200 for the public
page and readiness endpoint, UNAUTHENTICATED for anonymous identity/admin
queries, and HTTP 403 for a foreign-origin request. Registration is still
disabled. These checks do not establish Gmail delivery, authenticated file
workflows, database migration state, or production recovery. No live mutations
or emails were submitted. See [release checklist](release-checklist.md).
