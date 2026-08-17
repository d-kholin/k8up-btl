# Deploying k8up btl (in-cluster)

Runtime image: **`ghcr.io/d-kholin/k8up-btl`** (built by GitHub Actions from this repo).

## Prerequisites

- Cluster with Longhorn (or another RWO StorageClass named `longhorn`, or edit `pvc.yaml`)
- K8up operator + Secret `k8up/k8up-global` (see `k3s-hl` `docs/backups.md`)
- Argo CD in namespace `argocd` (for restore orchestration)
- Optional: Newt/Pangolin for external access

## Apply manifests

```bash
kubectl apply -k deploy/k8s
kubectl -n k8up-btl rollout status deploy/k8up-btl
```

Creates:

- Namespace `k8up-btl`
- SA + ClusterRole/Binding (K8up CRDs, scale workloads, Argo Applications, secrets)
- PVC `k8up-btl-data` (SQLite audit only)
- Deployment + Service `k8up-btl` (**ClusterIP port 80 → 8080**)
- NetworkPolicies: default-deny ingress, allow from `newt` only

No app-binary PVC and no seed pod. The container image holds `server`, SPA, and `restic`.

### Pin a release

Edit `deploy/k8s/kustomization.yaml`:

```yaml
images:
  - name: ghcr.io/d-kholin/k8up-btl
    newTag: "0.1.0"   # or digest: sha256:...
```

Or:

```bash
cd deploy/k8s
kustomize edit set image ghcr.io/d-kholin/k8up-btl:sha-abc1234
kubectl apply -k .
```

### Image pull

Package is intended **public** (`ghcr.io/d-kholin/k8up-btl`). No pull secret.

If a private package remains, create once:

```bash
kubectl -n k8up-btl create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username=d-kholin \
  --docker-password=GITHUB_PAT_WITH_READ_PACKAGES
# and add imagePullSecrets: [{name: ghcr-pull}] on the Deployment
```

Pin releases via `deploy/k8s/kustomization.yaml` `images.newTag` (e.g. `1.0.0`).

## Publish image (CI)

Push to `main` or tag `v*` runs `.github/workflows/docker-publish.yml`:

- Multi-arch: `linux/amd64`, `linux/arm64`
- Tags: `latest` (main), `sha-<short>`, branch, semver from tags

Manual:

```bash
gh workflow run docker-publish.yml
```

Local (optional):

```bash
docker build -t ghcr.io/d-kholin/k8up-btl:dev .
docker push ghcr.io/d-kholin/k8up-btl:dev
```

## Pangolin (Newt) resource

| Field | Value |
|-------|--------|
| Method / scheme | **http** |
| Hostname | `k8up-btl.k8up-btl.svc` or `k8up-btl.k8up-btl.svc.cluster.local` |
| Port | **80** |
| Public hostname | e.g. `k8up.your.domain` |
| Auth | Pangolin resource protection only |

## Auth model

**No in-app login.** Access control is Pangolin (SSO/resource rules) + the
in-cluster NetworkPolicy (ingress only from `newt`).

The API never returns 401 for missing identity. Audit rows use
`AUTH_DEFAULT_USER` (default `operator`), or a proxy header if present.

## SQL dump recovery (per-app setup)

K8up takes application-level dumps via the `k8up.io/backupcommand` pod
annotation. K8up has no inverse operation, so k8up btl restores them itself:
it pipes `restic dump <snapshot> <file.sql>` into the DB pod over `pods/exec`,
running a restore command that **must be declared in git** — the GUI never
accepts a free-text command. During recovery only the app's own workloads are
stopped (grouped by `app.kubernetes.io/instance`); other apps sharing the
namespace keep running, and the DB pod stays up to receive the pipe.

Add the annotation to the DB workload's **pod template**, next to the K8up
backup annotations. It runs via `sh -c` in the backup container, so the pod's
own env vars (credentials) are available:

```yaml
# PostgreSQL (pair with pg_dump --clean --if-exists in the backupcommand so
# restores into a non-empty DB replace objects instead of colliding)
k8up.io/backupcommand: sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --clean --if-exists -U "$POSTGRES_USER" "$POSTGRES_DB"'
k8up.io/file-extension: .sql
k8up-btl.local/restore-command: psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"

# MariaDB
k8up.io/backupcommand: sh -c 'mariadb-dump -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" --databases "$MARIADB_DATABASE"'
k8up.io/file-extension: .sql
k8up-btl.local/restore-command: mariadb -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" "$MARIADB_DATABASE"

# MySQL
k8up.io/backupcommand: sh -c 'mysqldump -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" --databases "$MYSQL_DATABASE"'
k8up.io/file-extension: .sql
k8up-btl.local/restore-command: mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"
```

Optional — override which workloads are stopped during recovery (defaults to
everything sharing the pod's `app.kubernetes.io/instance` label, excluding the
DB workload itself):

```yaml
k8up-btl.local/quiesce-workloads: deployment/mealie,statefulset/mealie-worker
```

The recovery dialog also offers to restore the app's other PVCs to the
snapshot nearest the dump's timestamp, so the whole app moves to one point in
time. A fresh safety backup is taken (while quiesced) before any data is
touched, unless explicitly skipped.

**RBAC required:** `pods/exec` `create` (see `rbac.yaml`). This lets the
backend exec into pods cluster-wide — a real privilege escalation over the
previous read+scale surface. It is accepted for this single-operator tool
behind SSO; if that is not acceptable in your environment, omit the rule and
SQL recovery will fail cleanly while everything else keeps working.

## Verify

```bash
kubectl -n k8up-btl get deploy,svc,pvc
kubectl -n k8up-btl get pods -o wide
kubectl -n newt exec deploy/newt-site-a -- wget -q -O- -T 5 http://k8up-btl.k8up-btl.svc/healthz
kubectl -n k8up-btl port-forward svc/k8up-btl 8080:80
# curl http://127.0.0.1:8080/api/v1/schedules
```

## Migrating from `k8up-gui` names

Cluster resources were previously namespaced as `k8up-gui`. After GitOps
switches to `k8up-btl`, copy the audit SQLite and retarget Pangolin:

```bash
# After Argo has created ns/deploy/pvc k8up-btl and k8up-btl-data is Bound:
kubectl -n k8up-btl scale deploy/k8up-btl --replicas=0
kubectl -n k8up-gui scale deploy/k8up-gui --replicas=0 --ignore-not-found

# Temporary mount pods (one per namespace — PVCs are not cross-namespace)
kubectl -n k8up-gui run k8up-old-data --restart=Never --image=busybox:1.36 \
  --overrides='{"spec":{"containers":[{"name":"c","image":"busybox:1.36","command":["sleep","3600"],"volumeMounts":[{"name":"d","mountPath":"/data"}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"k8up-gui-data"}}],"securityContext":{"fsGroup":65532}}}'
kubectl -n k8up-btl run k8up-new-data --restart=Never --image=busybox:1.36 \
  --overrides='{"spec":{"containers":[{"name":"c","image":"busybox:1.36","command":["sleep","3600"],"volumeMounts":[{"name":"d","mountPath":"/data"}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"k8up-btl-data"}}],"securityContext":{"fsGroup":65532}}}'
kubectl -n k8up-gui wait --for=condition=Ready pod/k8up-old-data --timeout=60s
kubectl -n k8up-btl wait --for=condition=Ready pod/k8up-new-data --timeout=60s

kubectl -n k8up-gui cp k8up-old-data:/data/. /tmp/k8up-btl-data-migrate/
kubectl -n k8up-btl cp /tmp/k8up-btl-data-migrate/. k8up-new-data:/data/
rm -rf /tmp/k8up-btl-data-migrate

kubectl -n k8up-gui delete pod k8up-old-data --wait=false
kubectl -n k8up-btl delete pod k8up-new-data --wait=false

# If image is still private, copy pull secret into the new namespace:
# kubectl get secret ghcr-pull -n k8up-gui -o yaml | sed 's/namespace: k8up-gui/namespace: k8up-btl/' | kubectl apply -f -

kubectl -n k8up-btl scale deploy/k8up-btl --replicas=1
kubectl -n k8up-btl rollout status deploy/k8up-btl
# Pangolin backend host → k8up-btl.k8up-btl.svc:80
# After verify: delete Application leftovers / ns k8up-gui (and orphan PVC).
```

## Migrating off the old PVC-seeded deploy

If you previously used `k8up-gui-app` + seed pod (legacy):

```bash
kubectl -n k8up-btl scale deploy/k8up-btl --replicas=0
kubectl apply -k deploy/k8s
# optional cleanup once the new pod is healthy:
kubectl -n k8up-gui delete pvc k8up-gui-app --ignore-not-found
kubectl -n k8up-gui delete pod k8up-gui-seed --ignore-not-found
kubectl -n k8up-btl rollout status deploy/k8up-btl
```

Keep the **data** PVC (`k8up-btl-data`) — that is the audit SQLite volume.

## Notes

- RWO data volume → single replica, `Recreate` strategy.
- Image is distroless/static + nonroot (uid 65532); root FS read-only.
- Ephemeral smoke `deploy/ephemeral-smoke.yaml` is obsolete; prefer `deploy/k8s/`.
