# Operations and recovery

## Status and deployment boundary

The repository includes private API/collector Prometheus endpoints, ten validated
alert rules, a local encrypted backup script, and an isolated restore verifier.
These assets are not a deployed production service. Configure an actual hostname,
TLS certificate, private PostgreSQL, storage class, image registry, monitoring
receiver, and backup destination before production deployment.

## Monitoring

Run the isolated Compose application with its monitoring profile:

```powershell
docker compose -p full-stack-file-vault-application -f compose.application.yaml --profile monitoring up -d
```

Prometheus is available only on localhost:9090. API/collector metrics are internal
ports 8080/8082; the public web proxy does not route to them. Kubernetes PodMonitor
configuration requires the Prometheus Operator and a matching monitor selector.
Do not expose operational ports through public ingress.

Metrics use bounded route, status-class, and error-code labels. They must never
include filenames, sharing/download tokens, usernames, session IDs, or headers.
The collector reports successful cycles and removal attempts; a retry removing an
already absent generation is not a unique physical deletion.

Investigate API availability and database connectivity first for outages.
For cleanup failures, check worker credentials, filesystem permissions, free space,
and retained intents. Do not delete tombstones or upload receipts to silence an
alert: they protect exact-generation cleanup and replay semantics.
Capacity alerts require kubelet volume metrics in Kubernetes.

The backup freshness alert intentionally fires until a trusted backup job exports
`vault_backup_last_success_seconds`. Publish that timestamp only after verifying
the off-host object and completing the applicable restore check. A local archive
alone is not an off-host backup. Configure Alertmanager routing and test delivery
with the operator; no notification destination is configured here.

## Consistent encrypted local backup

```powershell
node scripts/backup-application.mjs
node scripts/verify-backup.mjs tmp/backups/<generated-directory>/backup.enc
node scripts/verify-application.mjs --backup
```

These commands target only the isolated `full-stack-file-vault-application` Compose project.
The backup script verifies its file volume, stops web/API/collector writers,
captures PostgreSQL in custom dump format plus the entire file volume, and
restarts services. It bundles both with a manifest and encrypts using AES-256-GCM,
a random nonce, an authenticated format header, and a separate 256-bit key.
Plaintext temporary files are removed in cleanup; failed archives are removed.
If automatic service restart fails, explicitly run the Compose start command
and investigate before accepting further writes.

The ignored key is `.secrets/backup-key`; archives are under ignored `tmp/backups`.
On Windows, POSIX mode bits are not a substitute for NTFS ACLs: restrict these
directories to the operator account. The key and backup must not share the same
failure domain. Losing the key makes recovery impossible. This local key file is
a rehearsal mechanism, not production key management.

The verifier authenticates the whole archive before extraction, rejects tampering,
restores PostgreSQL into a disposable container without networking, checks schema
version and logical quota invariants, and checks SHA-256 and size for every blob
referenced by restored files. It removes only its generated container and
temporary directory. Reports contain aggregate counts, not user content.
The HTTPS smoke test with `--backup` supplies a real uploaded file before running
the backup and restore checks. This does not yet prove a complete browser session
against a restored application deployment.

## Production destination: private encrypted object storage

Use a dedicated private bucket/container with public access disabled, TLS-only
access, encryption through the provider's managed key service, versioning and a
reviewed retention policy. Keep client-side encryption as an additional layer if
required by the deployment. Use workload identity rather than committed access
keys. Separate backup writer and restore reader identities; the application
runtime should have neither permission. Protect encryption-key recovery separately.

A deployment-specific backup job must quiesce every writer, including operator
jobs, or implement coordinated database/file snapshots. Capture both stores,
encrypt, upload under a unique immutable identifier, verify object length and
checksum, then publish backup freshness. Never independently restore a database
and a file archive from unrelated captures. Enable retention/immutability according
to recovery and deletion obligations; a reasonable initial proposal is daily
backups with 30-day retention, subject to operator approval and capacity testing.

The selected provider, bucket, region, key identifier, workload identity and
retention policy have not been supplied. No remote bucket is created and no
backup has been uploaded. Do not place credentials in command arguments or logs.

## Restore procedure

1. Select a verified paired backup and recover its encryption key through the
   independent key-recovery process.
2. Download to private scratch storage and verify authenticated decryption before
   reading archive content. Keep the active application offline during replacement.
3. Restore required database roles, then the custom dump into a fresh database;
   restore the matching file archive to a new persistent volume preserving Linux
   ownership and permissions. Do not reuse an unrelated live file volume.
4. Check migration version, quotas, referenced blob sizes and SHA-256 hashes.
   Rotate deployment credentials independently of restored data.
5. Start the matching application image against the restored stores. Verify
   login, owned and shared downloads, revocation, upload replay, and cleanup.
   Decide whether to revoke restored sessions and links following an incident.
6. Record measured recovery time, recovery point, test results, image identity,
   and off-host object identity before switching traffic. Keep the previous stores
   until the recovered environment passes acceptance.

Production RPO/RTO, alert delivery, off-host recovery and a full restored-browser
rehearsal remain acceptance tasks; local script success does not establish them.
