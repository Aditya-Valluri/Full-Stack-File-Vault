# ADR 0007: Additive account identities and verified registration

Status: Identity foundation and verified email/password registration implemented.
Password reset/change are implemented and validated with isolated PostgreSQL and browser tests. Real Gmail recovery delivery remains unverified. Google OIDC remains pending.

## Existing accounts and scope

Preserve vault.users IDs, file ownership, sessions, CSRF, role checks, request
admission, and vault.credentials username/password hashes. Never infer an email
from an existing username. Legacy username login remains available.

For this project a user has at most one verified account email. Nullable
email_address, email_normalized, and email_verified_at fields on vault.users
avoid a separate multi-email table. All three are absent for legacy accounts.
A unique index reserves normalized verified addresses. New identity methods live
in vault.user_identities, unique by provider/subject and by user/provider.
Password identities require a password hash; Google identities forbid one.
This foundation does not add general account INSERT/UPDATE privileges to runtime.
Future verified operations must use narrowly scoped database functions.
Rollback refuses to discard identities or verified emails.

## Identity policy

Email comparison will explicitly use a documented case-insensitive policy for
supported addresses while retaining the delivery spelling. Do not strip dots or
plus suffixes. A uniqueness conflict must never silently reassign an account.
Google identity is the verified issuer's stable sub, not its email claim.
Never merge or link accounts merely because emails match. A new Google subject
whose email is already reserved must not gain access to the existing user.
Explicit linking is deferred and requires reauthentication of both methods.

## Implementation slices

1. Add nullable verified-email fields and an identity table. Exercise existing
   username/session/authorization tests against the migrated schema.
2. Add email challenges, delivery, verified registration and email/password login.
   Flow: enter email, check inbox, verify ownership, set and confirm password,
   activate account. Pending requests cannot establish an attacker-chosen password.
3. Add Google OIDC using established OAuth/OIDC libraries, state, nonce, PKCE,
   signature/issuer/audience/expiry validation and replay protection.
4. Add password reset and password change after the earlier slices pass tests.

## Security requirements for later slices

Keep Argon2id and bounded hashing concurrency. Public registration always creates
USER accounts with 10,000,000-byte logical quota. Unknown emails and wrong
passwords must have equivalent generic responses; registration/reset must not
reveal account existence. Apply separate bounded authentication/email budgets
without weakening the existing per-user rolling limit.

Issue existing opaque session cookies after authentication, rotate session/CSRF
credentials and store only session-token hashes. Revoke sessions on password
change/reset and retain logout/account-disable semantics. Preserve the existing
session-restoration behavior and protected multipart transport.

Google requests only openid and email, not Gmail or profile APIs. Discard Google
access/ID tokens after validation; do not request offline access or retain refresh
tokens. Store minimal identity data; never expose provider subjects in public
sharing or normal user listings. No automatic account linking.

Keep application operations in GraphQL. OAuth redirect/callback transport is
separate and browser-bound; it does not bypass GraphQL Origin/CSRF protections.
Authorization codes/state necessarily appear in standard OAuth redirect transport;
never log them, and redirect immediately to a clean application URL. Reset and
verification credentials will be entered on a clean page rather than URL queries.

One-time challenges are random, short-lived and stored only as hashes. Redemption
must be atomic and purpose-specific. Expired attempts/challenges require bounded
cleanup. Production needs an HTTPS email provider; development mail capture must
be loopback-only and rejected by production configuration. No token logging.

Google credentials and mail provider keys belong in deployment secrets. Callback
URLs derive from PUBLIC_ORIGIN. Never expose server secrets through frontend build
variables. Provider activation, live email delivery, and real Google callback
validation require separate deployment checks.

## Verification and deployment

Foundation tests check migration round trips, unchanged legacy accounts, unique
verified emails/provider subjects, credential-kind constraints, refusal of data-
destructive rollback, and no new direct runtime account creation privileges.
The existing full integration suite then checks username login, cookie/session
rotation, logout, file isolation, sharing, quota/rate enforcement and administration.

No Render identity, plan, database name/user, secret, or live resource changes are
part of this work. Registration requires a configured mail sender.
Migration 000017 adds hashed 15-minute verification challenges and a narrow
account-creation function. Redemption and browser-session rotation are atomic.
Password reset/change are implemented and validated with isolated PostgreSQL and browser tests. Real Gmail recovery delivery remains unverified. Google OIDC remains pending.
