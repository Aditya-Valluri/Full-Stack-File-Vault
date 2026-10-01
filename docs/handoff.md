# Handoff — 2026-10-01

## Release status

Ready for operator-led release validation, not certified for production operation.
A healthy public page does not establish a tested authenticated deployment.

Implementation dab56bf passed complete hosted CI. Review corrections ef36ab5 and
the recovery-tool fixes accompanying this document require their own green CI.
No Render deployment, Blueprint sync, credential rotation, resource creation,
real email or live database change was performed during this handoff.

## Evidence

| Check | Result and limit |
| --- | --- |
| Backend/browser | Implementation CI passed Go race/unit/integration/vet, frontend checks, 18 browser scenarios, dependency audit, generated bindings, image builds and Kubernetes rendering. |
| Render container rehearsal | Passed 2026-10-01: non-superuser migrations, repeat setup, secure cookies, 10 MB limit, deduplication, sharing/admin, persistence across replacement, quota release and GC. Disposable resources removed. |
| Public reachability | Existing HTTPS page and /readyz returned 200 on 2026-10-01. Deployed revision, authenticated flows and mail remain unverified. |
| Encrypted recovery | Schema-15 backup restored into a disposable offline database: four files, four verified blobs, zero quota mismatches, tamper rejection. Not a latest-schema or off-host browser restore. |
| Recovery validator | Seven regression cases cover supported current/older schemas and rejection of unknown, dirty, malformed or quota-inconsistent state. Versions come from checked-in migrations. |
| Frontend build | Passes with a non-blocking main-bundle warning (about 579 KB minified). |
| Infrastructure | Kubernetes rendering and alert rule validation pass; cluster rollout, alert delivery and sustained load are not established. |

The PostgreSQL BlobStore remains bounded and deployment-specific. External object
storage is the production target. Google sign-in, real-time updates, admin graphs,
one-time links and Helm remain deferred.

## Operator release sequence

1. Confirm the final commit is green in GitHub Actions.
2. In Render, verify existing web service srv-daolnujbc2fs73fv5a20 and database
   dpg-daolnk3bc2fs73fv48b0-a against the dashboard. Web name:
   full-stack-file-vault-web. Preserve database Blueprint identity
   full-stack-file-vault-free-demo-db; its visible label may be
   full-stack-file-vault-db. Preserve free plans and database contents.
3. Keep Blueprint automatic sync paused. Never approve Create database or Create
   web service. Deploy the approved commit to the existing web service only,
   after verifying its database association.
4. Privately verify Gmail settings using [email setup](authentication-email.md).
   Use the dedicated sender and gmail.send only. Do not paste credentials into
   chat, tickets, screenshots, source or browser settings.
5. If enabling MFA, configure and independently back up its key using
   [MFA setup](authentication-mfa.md). Existing enrolled users require the same
   key; do not casually generate a replacement.
6. Confirm migrations through 000025, readiness and the exact HTTPS origin.
   Follow [release checklist](release-checklist.md) for controlled legacy/email
   login, one registration email, reset/change, logout/session revocation,
   administrator contact, uploads/duplicates/quota, folders/tags, previews,
   sharing/revocation and non-admin access denial. Confirm actual mail receipt.
   Stop on uncertain delivery; do not repeatedly send.
7. Record deployed SHA, service ID, test timestamp and sanitized outcomes.
   Never record codes, passwords, tokens or private database URLs.
8. Before claiming production readiness, complete a latest-schema browser restore,
   off-host backup/restore, alert delivery, sustained-load and infrastructure
   failure tests in the selected environment.

## Access still required

Live validation needs an operator with the existing Render dashboard and controlled
test accounts/inboxes. Off-host recovery needs a selected private storage destination
and independently protected keys. Cluster and alert checks need the actual cluster
and notification destination. These checks are not marked complete from local tests.

The redundant completion worktree was removed after retaining useful corrections.
A hash-verified local source snapshot remains under ignored
tmp/worktree-archive/completion-20261001-010820; it is not a release artifact.

## Agreed handoff priorities

1. **Live acceptance:** verify the deployed SHA, then login, registration, reset,
   upload and sharing using controlled accounts. Public readiness alone is not a
   pass. Use the release checklist; record outcomes without credentials or codes.
2. **Optional MFA:** enable enrollment only after configuring and separately
   backing up the matching MFA_ENCRYPTION_KEY. Do not regenerate an existing key.
   The current live MFA configuration has not been independently verified.
3. **Setup documentation:** follow the consolidated README quick-start for both
   development terminals, account setup and optional settings.
4. **Later maintenance:** frontend component extraction and bundle-size optimization
   are deferred. The bundle warning is not a failed build.

Google sign-in, real-time updates, admin graphs, one-time links, Helm, external
object storage, off-host recovery, alert delivery, sustained load and cluster
rollout are outside the user's current handoff scope. Preserve these limitations;
do not call them implemented or production-validated.
