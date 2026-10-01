# Public release checklist

This checklist targets the existing free Render deployment. It does not certify
production availability or authorize creating, replacing, or deleting resources.

## Verified locally

- Backend unit tests, static checks, and PostgreSQL integration tests passed.
- Eighteen browser scenarios passed, including MFA, folders, upload progress,
  view-only sharing, registration, password recovery,
  administrator contact, file operations, sharing, administration, accessibility,
  retry protection, and session restoration.
- Email tests use a local capture service. They do not prove real Gmail delivery.

## Release gates

1. Open GitHub Actions for the latest pushed commit. Confirm the complete workflow
   succeeds, including image builds, generated bindings, security checks, and
   Kubernetes manifest rendering. Local test success is not hosted CI evidence.
2. Keep Render Blueprint automatic sync paused. Preserve the web identifier
   full-stack-file-vault-web and database identifier full-stack-file-vault-free-demo-db.
   Verify their existing-resource association before any sync. Never approve
   Create database or Create web service for this existing deployment.
3. Confirm the existing web service and database IDs in Render. Deploy an approved
   commit to that existing service only. Keep both plans unchanged.
4. Configure these backend-only settings privately in the existing web service:
   AUTH_MAIL_MODE=gmail, AUTH_MAIL_FROM=fullstackfilevault@gmail.com,
   GMAIL_SENDER_CLIENT_ID, GMAIL_SENDER_CLIENT_SECRET, and
   GMAIL_SENDER_REFRESH_TOKEN. Never copy credential values into this checklist,
   source files, browser variables, or chat. Reuse the send-only authorization.
5. Confirm startup applies migrations through 000025 and readiness succeeds.
   Before enabling MFA, configure MFA_ENCRYPTION_KEY (or its _FILE source) privately
   and securely back it up separately from the database. See authentication-mfa.md.
   Use the actual HTTPS URL shown by Render, with the exact configured origin.
6. Verify the existing username account still signs in and signs out.
7. With explicit authorization, send one registration email to a controlled address.
   Confirm the sender, seven-digit code, same-browser verification, strong-password
   rejection, successful registration, logout, and email/password login.
8. Test signed-in password change. Confirm a new login is required and another
   active session is revoked. Avoid rapid repeated login attempts: the shared
   fifth consecutive failure starts temporary restrictions; recovery requests
   retain a separate five-per-15-minute identifier limit.
9. With explicit authorization, send one reset email for a verified account.
   Complete the reset in the requesting browser and log in with the new password.
   Do not automatically retry a failed or uncertain send.
10. With explicit authorization, submit one non-sensitive administrator contact
    message and verify receipt in fullstackfilevault@gmail.com. Provider acceptance
    is not proof of inbox delivery.
11. Upload small duplicate files, verify logical quota and storage statistics, search
    by name/tag, preview and download, share with another browser, revoke the link,
    and delete the files. Confirm non-admin accounts cannot access administration.
12. Record the tested commit, service ID, public URL, database expiry date, and
    outcomes without passwords, codes, session tokens, or private URLs.

## Outside the free public release

Google sign-in remains deferred. Real-time updates, admin graphs, one-time share
links and Helm are not implemented. Private folders and optional TOTP MFA are
implemented; see implementation-review.md for the current scope and verification. External object storage, production Kubernetes
rollout, alert delivery, off-host restoration, sustained load, and infrastructure
failure drills require separate implementation/configuration and validation.
The PostgreSQL content adapter remains bounded and deployment-specific; it is not
a replacement for the documented production storage target.

## Historical evidence (2026-09-28)

Hosted CI passed for 8af78c3 (run 36371269143). The updated container rehearsal
passed at 2026-09-28T03:00:07.189Z; see acceptance.md for recorded coverage.
The supplied Render dashboard confirms commit 8af78c3 is Live on existing service
srv-daolnujbc2fs73fv5a20. Public page and readiness checks returned HTTP 200.
Anonymous identity/admin requests returned UNAUTHENTICATED; a foreign-origin
request returned HTTP 403. Registration remains disabled as of the latest check.
Gmail configuration and authenticated public workflows remain to be validated.


## Current handoff

See [handoff](handoff.md) for the 2026-10-01 evidence and remaining operator checks.
Historical observations above do not establish current Gmail settings or revision.
