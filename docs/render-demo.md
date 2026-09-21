# Free temporary hiring demo on Render

## Scope and limitations

The root render.yaml requests one Free web service and one Free PostgreSQL 17
database. There is no paid disk, pre-deploy job, AWS service or paid workspace
requirement in this configuration. This is a temporary reviewer environment.

Render Free web services sleep after 15 minutes without traffic; waking takes
about a minute. Their local files disappear on sleep, restart and redeploy.
The demo-only PostgreSQL BlobStore keeps final file bytes in the database, so
losing the web container's staging directory does not lose published files.
The database has a 1 GB allowance and expires 30 days after its creation.
The demo does not renew that period on deployment or restart.

Use sample files only. This is not a permanent backup service or a production
availability promise. See [ADR 0006](decisions/0006-temporary-postgres-demo-storage.md)
for the adapter contract, size limits, privileges and production object-storage target.

## Prevent unexpected charges

1. Use a Hobby workspace and verify both proposed resources explicitly show Free.
2. The Blueprint contains no chargeable disk and no database diskSizeGB override.
3. Check Billing for existing paid resources from earlier attempts. Creating this
   free demo does not cancel them. Do not delete anything without exporting data
   you need; no existing resource is removed by this repository change.
4. Render documents that, without a payment method, exhausted bandwidth suspends
   free services and exhausted build minutes disables further builds. With a
   payment method, usage overages can be billed. Check the account's actual billing
   settings and allowances rather than assuming plan: free is a universal cap.
5. Do not add paid resources or upgrade the expiring database to keep this demo alive.
   No artificial keep-alive polling is installed.

The GitHub commit cannot change your Render account billing settings. If the
dashboard requires payment or shows any nonzero resource price, stop before
creation and check the selected plan. Account/provider policies can change.

Sources: [Free instances and overages](https://render.com/docs/free),
[pricing](https://render.com/pricing).

## Deploy a fresh demo

1. Open https://dashboard.render.com and connect GitHub to
   Aditya-Valluri/Full-Stack-File-Vault. Do not paste API keys or passwords into chat.
2. Choose New > Blueprint, select main and the root render.yaml.
3. Use a new Blueprint, for example File Vault Free Demo. Resource names are
   file-vault-free-demo and file-vault-free-demo-db. Do not sync the old paid
   Blueprint as an in-place migration: disk contents are not copied into PostgreSQL.
   A workspace supports only one Free PostgreSQL database; resolve an existing
   free-database conflict before creation, without discarding needed data.
4. Confirm both plans show Free, then create the Blueprint.
5. Startup applies normal migrations, installs the separate demo byte-storage
   schema, and creates reviewer accounts. Setup runs on every wake and is
   repeatable; free services do not have a pre-deploy phase or dashboard shell.
6. Wait for Live and copy the actual assigned HTTPS URL. Render provides TLS;
   PUBLIC_ORIGIN defaults to RENDER_EXTERNAL_URL.
7. Retrieve generated passwords from the web service's Environment settings:
   - reviewer: DEMO_REVIEWER_PASSWORD
   - reviewer-admin: DEMO_ADMIN_PASSWORD
8. Share the verified URL and reviewer credentials privately. The normal local
   safeandsecure account and its password are not copied to this database.

The independent DEMO_RUNTIME_SEED and DEMO_GC_SEED derive stable restricted
database passwords. Do not regenerate these or reviewer passwords casually.
Existing accounts with changed passwords/roles/disabled status cause setup to
fail rather than silently overwriting operator decisions. Correct the account or
configuration through an explicitly authorized workflow.

Auto-deploy is off. Use manual deployment after a reviewed change. A Blueprint
sync may still change infrastructure independently of that setting. Use the same
HTTPS origin throughout; a later custom domain requires exact PUBLIC_ORIGIN.

## Limits and operations

The adapter is explicitly selected with BLOB_STORAGE_BACKEND=postgres-demo in
the demo supervisor's child environment. Production defaults to local storage.
There is no fallback from a missing/corrupt database object to a local file.

The demo limits each file to 10 MB decimal, each request to 11 MB including
multipart overhead, and concurrent uploads to two. Normal per-user logical quota
and rolling 2 calls/sec remain. Physical content has a shared 100 MB/10,000-object
cap, enforced in PostgreSQL, including orphan objects awaiting cleanup. New bytes
are also rejected once total database size reaches 650 MB, leaving headroom.
These checks cannot bound all metadata/WAL growth; monitor database usage.

API and collector share a container but get separate restricted database URLs.
The supervisor still has operator credentials. This is not container isolation
between workers. Only the public gateway is exposed; API/collector ports are
loopback. The image runs as UID 65532; /data contains ephemeral staging only.

If startup fails, inspect fixed error messages in Render logs. The database owner
must have permission to create the restricted roles. The local rehearsal exercises
a non-superuser CREATEROLE owner; actual managed-database privileges remain a cloud
acceptance check. Never run the API under the operator login as a workaround.

If setup reports disk-backed blobs, use a fresh database; do not remove those
records to bypass the check. If the database expired, exporting/recovery may no
longer be available under the Free plan. Arrange review before the expiration
shown in Render's database dashboard.

## Public acceptance checks

- Trusted HTTPS loads without accepting a certificate warning.
- The temporary-demo notice is visible.
- Sign in as reviewer and refresh; the username remains correct.
- Upload docs/demo-samples/review-notes.txt and review-notes-copy.txt.
  Observe two logical files and 50% unique-content savings.
- Add private tags; search by tags/uploader.
- Download and compare bytes; create a public share and open it in another browser.
- Redeploy/restart the service, then verify original downloads and shares still work.
- Delete one duplicate and download the remaining copy; delete the final copy and
  verify logical quota release. Physical cleanup follows the normal grace period.
- Revoke a share and verify subsequent access is denied.
- Sign in as reviewer-admin separately and inspect Administration > All files.
- Confirm no private metrics are exposed at the public /metrics path.
- Record the actual public URL only after these checks pass.

## Local rehearsal

Requires Docker, installed frontend dependencies, Playwright Chromium, and the
existing local TLS files created by scripts/initialize-application.ps1.

```powershell
docker build -f deploy/render/Dockerfile -t file-vault-render:local .
node scripts/verify-render-demo.mjs
```

The rehearsal uses uniquely named disposable containers and a synthetic database.
The app runs with a 512 MB memory limit and ephemeral /data. It is destroyed and
recreated, proving recovery without retained local final files. The real API,
collector and browser exercise deduplication, sharing, accounts, downloads and
deletion. It does not modify the existing localhost app or account.
Aggregate results and a synthetic screenshot go under ignored tmp.

Go/PostgreSQL integration tests additionally cover file-size boundaries, concurrent
capacity admission, immutable generations, privilege separation and quota rollback.
Cloud deployment, account billing and database expiration are not simulated by Docker.

## Reviewer handoff

Send the actual verified URL, reviewer username and password privately, together
with the GitHub repository and access if private. Include the expiry date shown
by Render and mention that the first visit can take about a minute to wake.

Suggested message:

> File Vault temporary demo: [verified URL]. Please use the reviewer credentials
> supplied separately and sample files only. The demo is available until [date],
> and its first page load after inactivity may take about a minute.
> Try duplicate uploads, private tags/search, downloads and revocable sharing.
> Administrator credentials are available for reviewing controls and audit logs.

The production-oriented Linux volume deployment remains documented separately.
Durable external object storage is the production target. This demo does not
claim off-host backup, high availability or a tested disaster-recovery workflow.
