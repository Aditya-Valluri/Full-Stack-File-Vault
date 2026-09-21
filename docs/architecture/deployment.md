# Deployment

The application images run as non-root users with read-only root filesystems.
API, collector, and operator commands read credentials from explicitly mounted
NAME_FILE secrets. Configuring both NAME and NAME_FILE is rejected. Credentials
are not embedded in image layers or accepted as migration command arguments.

## Local production-mode rehearsal

From the repository root in PowerShell:

```powershell
./scripts/initialize-application.ps1
docker compose -p full-stack-file-vault-application -f compose.application.yaml up -d --build --wait
node scripts/verify-application.mjs
```

The rehearsal uses https://localhost:8443 and an ignored, seven-day self-signed
localhost certificate. Only the automated localhost check bypasses certificate
trust. Browser users must trust an appropriate development certificate. Do not
use this certificate for an Internet deployment.

The explicit project name isolates these volumes from the existing development
database. Never replace that command with a different project name to target
an existing installation unintentionally. PostgreSQL is not exposed on a host
port. API port 8080 is internal; only the web TLS listener is published on loopback.

The migration container applies forward migrations and configures the restricted
runtime and collector role passwords from ignored secret files. The API starts
only after migration success. No default application account is created.
Provision accounts with the operator-only provision service, piping password
bytes through stdin; do not put passwords in arguments or commit them.

The helper preserves existing credentials and refuses mismatches. On Linux,
protect the host secret directory with mode 0700 and ensure each mounted secret
is readable by its designated non-root container UID. Docker Compose file-backed
secrets use bind-mount permission semantics.

To stop the rehearsal while retaining its data:

```powershell
docker compose -p full-stack-file-vault-application -f compose.application.yaml down
```

Do not add --volumes unless you intend to destroy this rehearsal's data.

## Kubernetes release preparation

The base manifests intentionally use local image tags and vault.example.com.
Before a real release, create an environment overlay that supplies:

1. Published API and web image digests, including the migration Job image.
2. The actual HTTPS hostname in PUBLIC_ORIGIN and Ingress hosts/TLS hosts.
3. A supported Traefik installation in namespace traefik (or adapt routing and
   NetworkPolicy to the platform's supported controller). Access logs must not
   record opaque content URLs. A dedicated-controller values example is provided.
4. A trusted TLS certificate in full-stack-file-vault-tls.
5. Separate full-stack-file-vault-runtime, full-stack-file-vault-gc, and full-stack-file-vault-operator Secrets,
   each containing database-url. Use a secret manager or --from-file from a
   private file, never a committed Secret manifest or a command-line literal.
6. A PostgreSQL service with backups and TLS certificate verification for remote
   connections. Provision restricted role login credentials through operator
   access after migrations; never run the API with the operator role.
7. A qualified Linux filesystem-backed PVC, and a narrowed PostgreSQL egress CIDR.

Create the namespace and secrets, run a uniquely named migration Job from
kubernetes/migrate-job.yaml, wait for successful completion, and then apply the
application overlay. A failed/dirty migration blocks release; do not force the
migration version or run automatic down migrations to hide a failure.

The API, web, and collector share a pod network; only API/collector mount the
file volume. The Deployment deliberately uses one replica and Recreate. RWO is
a mounting capability, not an application-level multi-writer fence. Do not add
an HPA or independent file disks. Qualify a storage redesign before scaling
application replicas across nodes.

Readiness checks verify the runtime database role and current feature tables.
Liveness only checks process responsiveness. The collector emits bounded cycle
results; it must not be restarted merely because one cleanup cycle is delayed.

## CI and release boundaries

CI runs formatting, race-enabled Go unit tests, vet, database integration tests,
frontend generation/build/unit/audit/browser checks, image builds, and manifest
rendering. Official Actions are pinned to immutable commits. CI builds images;
it does not publish images or deploy a cluster automatically.

Local rendering is not proof of a working Kubernetes deployment. Cluster admission,
PVC driver semantics, actual TLS/DNS, network policy enforcement, and backup
restoration must be validated in the target environment before launch.

References: [Kubernetes persistent volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/),
[Traefik access logging](https://doc.traefik.io/traefik/observe/logs-and-access-logs/),
[Ingress NGINX retirement](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/).
