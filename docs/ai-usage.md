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
