# Getting started — adding k8up btl to an existing K8up setup

You already run [K8up](https://k8up.io/) and want the GUI on top. This guide
covers what k8up btl assumes about your cluster, what to edit before applying
the manifests, and how to verify each feature tier works. Homelab-specific
deployment details (Pangolin/Newt, image pinning, CI) live in
[`deploy.md`](deploy.md).

## What you need — by feature tier

k8up btl degrades gracefully: each tier works without the ones below it.

| Tier | Features | Requires |
|------|----------|----------|
| **Visibility** | Schedules, snapshots, job status, calendar, health, notifications | K8up v2 with the Snapshot CRD populated (`kubectl get snapshots.k8up.io -A` shows your backups) |
| **Browse** | Snapshot file tree, file/folder download, snapshot diff | Restic repository credentials readable by the backend (see step 2) |
| **Restore** | One-click PVC restore with pause/scale orchestration | A K8up `Schedule` in each namespace (backend + pod security are inherited from it — step 3) |
| **SQL recovery** | Point-in-time recovery of `.sql` dump snapshots | Everything above + `pods/exec` RBAC + `k8up.io/backupcommand` annotations on your DB pods (you likely have these already) |

**Argo CD is integrated but not required.** On Argo-managed clusters the
orchestrator pauses reconciliation (scales the
`argocd-application-controller` StatefulSet to 0) before touching any
workload and resumes it afterwards, so self-heal can't scale an app back up
mid-restore. On clusters without Argo CD the pause is skipped automatically
(logged in the restore output). **If a different GitOps controller manages
your replica counts** (Flux, etc.), suspend its reconciliation of the target
app yourself before restoring — nothing pauses it for you.

## Step 1 — edit the manifests

Copy `deploy/k8s/` into your own GitOps repo (recommended) or edit in place.
Four things are environment-specific:

1. **Image tag** — `kustomization.yaml` ships with a `1.x.x` placeholder:

   ```yaml
   images:
     - name: ghcr.io/d-kholin/k8up-btl
       newTag: "1.7.3"   # pick a release; Renovate-friendly
   ```

2. **Storage class** — `pvc.yaml` requests a 1Gi RWO volume (SQLite audit log
   only) using the cluster's default StorageClass. Uncomment
   `storageClassName` to pin a specific one.

3. **Argo CD namespace** — `deployment.yaml` sets `ARGOCD_NAMESPACE: argocd`.
   Change it if your Argo CD lives elsewhere; on clusters without Argo CD it
   can stay as-is (the pause step is skipped when the controller is absent).

4. **NetworkPolicy** — `networkpolicy.yaml` is default-deny plus an allow rule
   for a namespace named `newt` (a Pangolin tunnel). Replace the
   `namespaceSelector` with your ingress controller / auth proxy namespace —
   or delete both policies if you don't use NetworkPolicy. **Do not expose the
   Service without an authenticating proxy in front** (see step 5).

Also skim `rbac.yaml` before applying — it is cluster-scoped and includes
`secrets` read (restic credentials), `deployments/statefulsets` patch
(scale-down orchestration), and `pods/exec` create (SQL recovery pipes the
dump into your DB pod). If SQL recovery is not for you, remove the `pods/exec`
rule; that feature fails cleanly and everything else keeps working.

## Step 2 — restic credentials

For Browse, downloads, and SQL recovery the backend runs `restic` itself
against your repositories. It resolves the repository URI from each Snapshot
CR (`spec.repository`) and the credentials from **one Secret** referenced by
env on the deployment:

```yaml
- name: K8UP_GLOBAL_SECRET_NAMESPACE
  value: k8up            # change to your K8up operator namespace
- name: K8UP_GLOBAL_SECRET_NAME
  value: k8up-global     # change to your secret's name
```

The secret must contain these keys (the same names K8up's global-backend
operator config uses):

| Key | Meaning |
|-----|---------|
| `BACKUP_GLOBALREPOPASSWORD` | restic repository password |
| `BACKUP_GLOBALACCESSKEYID` | S3 access key |
| `BACKUP_GLOBALSECRETACCESSKEY` | S3 secret key |

If you configured K8up with global S3 credentials, point the env at that
existing secret and you're done. If you use **per-namespace credentials**,
the global secret is optional: when it's missing, the backend falls back to
the secret refs on each Snapshot CR (`spec.backend.repoPasswordSecretRef` and
`spec.backend.s3.accessKeyIDSecretRef`/`secretAccessKeySecretRef` — the same
refs your Schedules use). S3-compatible backends (AWS, MinIO, Garage, Ceph
RGW…) are what's tested; the endpoint is taken from the snapshot's repository
URI.

If neither source resolves a repository password, browse/download/recovery
return an error naming both places it looked, while visibility features keep
working.

## Step 3 — pod security for restore jobs (PSA-restricted namespaces)

Restore CRs and safety backups created by the GUI inherit `podConfigRef` and
`podSecurityContext` **from the namespace's K8up `Schedule`**, so one-shot
jobs run exactly like your scheduled ones. Nothing extra is needed unless a
namespace is labeled `pod-security.kubernetes.io/enforce: restricted` — there,
your Schedule must reference a [`PodConfig`](https://k8up.io/) (any name) that
makes the restic job pods admissible; if your scheduled backups already pass
PSA, you already have this:

```yaml
apiVersion: k8up.io/v1
kind: PodConfig
metadata:
  name: backup-pod
  namespace: <your-app-namespace>
spec:
  template:
    spec:
      containers:
        - name: backup
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
      securityContext:
        fsGroup: 1000            # match your app's volume ownership
        fsGroupChangePolicy: OnRootMismatch
        runAsGroup: 1000
        runAsNonRoot: true
        runAsUser: 1000
        seccompProfile:
          type: RuntimeDefault
```

Then reference it from the Schedule (`spec.podConfigRef: {name: backup-pod}`).
GUI-created restores and safety backups pick up whatever `podConfigRef` /
`podSecurityContext` the Schedule declares (job-section fields win over
schedule-wide ones) — there is no GUI-specific name or config to maintain.

## Step 4 — apply and verify

```bash
kubectl apply -k deploy/k8s
kubectl -n k8up-btl rollout status deploy/k8up-btl

# Tier checks, in order:
kubectl -n k8up-btl port-forward svc/k8up-btl 8080:80
curl -s http://127.0.0.1:8080/readyz                      # cluster connectivity
curl -s http://127.0.0.1:8080/api/v1/schedules | head -c 300   # visibility
# then in the UI: open a snapshot → Browse (tests restic credentials)
```

For the Restore tier, do one **end-to-end test restore on a throwaway app
before you need it in anger** — it exercises Argo pause/resume, scale-down,
the `backup-pod` PodConfig, and RBAC in one shot. Restores are
server-enforced to only target the PVC a snapshot was taken from, and only
one restore runs at a time, so the blast radius of a test is the app you pick.

## Step 5 — expose it (auth warning)

**There is no in-app login.** Anyone who can reach the Service can browse
backups, download data, and trigger restores. Put it behind an authenticating
reverse proxy (Authelia, Authentik, oauth2-proxy, Pangolin…) and keep the
NetworkPolicy so only that proxy can reach the pod. The proxy may pass
identity headers (`AUTH_USER_HEADER`, default `X-authentik-username`) which
are used for audit attribution only — absent headers fall back to
`AUTH_DEFAULT_USER`.

## Step 6 (optional) — SQL dump recovery per app

If your DB pods already carry `k8up.io/backupcommand` +
`k8up.io/file-extension: .sql` annotations, point-in-time recovery works with
**zero further config** for postgres/mariadb/mysql: the restore command is
derived from the backup command, and the confirm dialog always shows the
exact command and which workloads will be stopped before you commit. Per-app
overrides and the quiesce-scoping annotation are documented in
[`deploy.md`](deploy.md#sql-dump-recovery-per-app-setup).

One flag to check in your existing annotations: use `pg_dump --clean
--if-exists` so a restore into a non-empty database drop-and-recreates
objects instead of colliding (mariadb-dump/mysqldump already emit `DROP TABLE
IF EXISTS` by default).

## Config reference (adoption-relevant)

| Variable | Default | Meaning |
|----------|---------|---------|
| `ARGOCD_NAMESPACE` | `argocd` | Where `argocd-application-controller` lives |
| `K8UP_GLOBAL_SECRET_NAMESPACE` / `_NAME` | `k8up` / `k8up-global` | Restic credentials secret (step 2) |
| `SCALE_DOWN_TIMEOUT` | `10m` | Max wait for workload pods to terminate |
| `RESTORE_TIMEOUT` | `2h` | Max wait for a restic restore job |
| `SAFETY_BACKUP_TIMEOUT` | `30m` | Max wait for the pre-recovery safety backup |
| `SQL_RESTORE_CMD_<ENGINE>` | _(empty)_ | Cluster-wide restore command override per engine |
| `SQL_RESTORE_REQUIRE_ANNOTATION` | `false` | Require the per-pod restore-command annotation |

The full env table (notifications, Prometheus, auth headers) is in the
[README](../README.md#config-env).
