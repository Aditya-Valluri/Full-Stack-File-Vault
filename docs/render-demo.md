# Publish the hiring demo on Render

## What is prepared

The root `render.yaml` creates one single-instance web service and one managed
PostgreSQL 17 database in Virginia. The web service runs the static UI gateway,
API and cleanup worker together because Render disks cannot be shared across
services. Only the gateway port is public; API and collector listeners are on
loopback. Existing GraphQL operations, CSRF/Origin checks, Secure cookies, quota,
rate limiting, private tags and sharing authorization remain enabled.

This is a reviewer demo architecture, not a horizontally scalable production
deployment. The processes share a container and service-level environment; separate
database roles reduce accidental privileges but do not provide container isolation
against a compromised supervisor. The API and worker child environments contain
only the respective restricted database URL. The supervisor/setup environment
still has operator credentials supplied by Render.

The image runs as UID/GID 65532. A writable persistent disk is mounted at /data,
with private staging/blob directories. A shell is included so Render can execute
the pre-deploy command; the normal process is the Go supervisor. It stops the
service if a worker exits and forwards shutdown to both workers.

## Cost approval before resource creation

As checked against Render documentation on September 20, 2026:

| Resource | Configuration | Approximate monthly cost |
| --- | --- | --- |
| Web compute | 0.5 CPU / 512 MB | $7.00 |
| PostgreSQL compute | 0.1 CPU / 256 MB | $6.00 |
| Uploaded-file disk | 1 GB | $0.25 |
| Database storage | 1 GB | $0.30 |
| Total | Hobby workspace, excluding tax/overages | $13.55 |

Review the actual Render cost summary before clicking Deploy/Apply. No paid
resources are created by committing this Blueprint or running local verification.
Remove the demo resources after the review if they are no longer needed; stop
sharing reviewer credentials then as well. Deletion permanently removes data, so
export anything needed first.

Sources: [pricing](https://render.com/pricing),
[compute plans](https://render.com/docs/compute-plans),
[storage billing](https://render.com/articles/how-much-does-cloud-application-hosting-cost-for-small-businesses).

## Deploy through your Render account

1. Sign in at https://dashboard.render.com and connect GitHub, granting access to
   `Aditya-Valluri/BalkanID-File-Vault`. Do not paste Render API keys or passwords
   into chat or repository files.
2. Choose **New > Blueprint**, select this repository and its main branch, and
   use the root `render.yaml`.
3. Review the proposed resources: `file-vault-demo`, `file-vault-demo-db`, both in
   Virginia, with the plans/storage above. If names already exist in your Render
   workspace, choose unique names consistently in the Blueprint before applying;
   do not unintentionally modify an unrelated service.
4. Approve the displayed charges and create the Blueprint. The build produces the
   combined image. The pre-deploy command `/app/render-demo setup` applies schema
   migrations, configures restricted logins, and creates reviewer accounts.
5. Wait for the service to show **Live** and copy the actual URL Render assigns.
   The code derives its exact HTTPS origin from `RENDER_EXTERNAL_URL`; no custom
   domain is required. Render supplies the trusted browser-facing TLS certificate.
6. Open the service's **Environment** settings to retrieve the generated demo
   passwords. Keep them private. The accounts are:
   - `reviewer`: value of `DEMO_REVIEWER_PASSWORD`
   - `reviewer-admin`: value of `DEMO_ADMIN_PASSWORD`
7. Test the public URL in a normal browser without accepting a certificate warning.
   Complete the checks below before sending the URL to the hiring manager.

The `DEMO_RUNTIME_SEED` and `DEMO_GC_SEED` are generated independently. Setup and
runtime derive stable 64-character database passwords from those random seeds;
do not regenerate them casually. Setup is repeatable and refuses to silently
overwrite an existing reviewer account whose password, role or enabled status
differs. A failed pre-deploy leaves the existing service deployment intact.

The database's external IP allow list is empty. Render services in the same
region use its private address. The demo requires TLS for database connections
by default; Render's internal self-signed database certificates support require,
not verify-full. A plaintext exception is used only by the isolated local test.

Auto-deploy is off. After new code is reviewed, use Render's manual deploy action
to deploy the selected commit. A disk-backed service has brief restart downtime.
If using a custom domain later, set PUBLIC_ORIGIN to exactly that HTTPS origin
and use it consistently; mixing the custom and onrender origins fails CSRF checks.

## Public acceptance checks

- Page loads over trusted HTTPS without a warning or redirect loop.
- Sign in as reviewer; username survives refresh.
- Upload both sample text files from docs/demo-samples; observe two logical files
  and 50% unique-content savings. Quota counts both references.
- Add private tags; search by tag and reviewer username.
- Download bytes; create a public link; test it in a separate browser session.
- Revoke the link and verify subsequent access is denied.
- Sign in as reviewer-admin in a separate session; inspect Administration > All
  files and storage usage. Administrative access does not reveal private tags.
- Restart the service once and verify files and sharing state persist.
- Review Render logs for failures without copying secrets, cookies or bearer URLs.
- Record the actual public URL in the README only after these checks pass.

The pre-deploy database owner must be able to create restricted roles and own the
database schema. The local rehearsal explicitly verifies migrations under a
non-superuser CREATEROLE database owner. If Render setup reports a permission
failure, inspect the managed database privileges; never switch the API to an
operator credential as a workaround.

If storage initialization fails, check the configured mount path and its UID
65532 write permissions. Do not weaken private directories to world-writable.
If the web service is unhealthy, inspect whether both API and collector started;
the public /readyz endpoint checks both.

## Local rehearsal

With Docker, Node dependencies, Playwright Chromium and the localhost certificate
from scripts/initialize-application.ps1 available:

```powershell
docker build -f deploy/render/Dockerfile -t file-vault-render:local .
node scripts/verify-render-demo.mjs
```

The rehearsal creates uniquely named temporary Docker resources, uses synthetic
credentials, and terminates HTTPS outside the application to model Render's edge.
It tests fresh setup, a non-superuser database owner, repeat setup, user/admin
login, uploads/deduplication, sharing, secure cookies, persistence across restart,
and the private metrics boundary. It does not create or validate a real Render
deployment. It does not modify the existing localhost application or safeandsecure
account. Aggregate results and a synthetic screenshot are saved under ignored tmp.

## Reviewer handoff

Send the actual verified demo URL, reviewer username and password through a
private channel. Offer the separate admin credentials when admin review is
needed. Include the GitHub repository URL and grant repository access if it is
private. Never publish demo passwords in README, issues or commit messages.

Suggested message:

> Here is the File Vault demo: [verified Render URL].
> Sign in with the reviewer credentials supplied separately. The repository
> includes setup instructions, GraphQL SDL, architecture notes and automated tests.
> Try the two sample uploads to see deduplication, then tags/search and link sharing.
> Administrator credentials are available for reviewing user controls and audit logs.

Cloud backups, notification routing and full production RPO/RTO qualification are
separate work. Render database backups alone do not constitute a consistent paired
database/file backup; see architecture/operations.md.
