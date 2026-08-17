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
kubectl -n k8up-gui rollout status deploy/k8up-gui
```

Creates:

- Namespace `k8up-gui`
- SA + ClusterRole/Binding (K8up CRDs, scale workloads, Argo Applications, secrets)
- PVC `k8up-gui-data` (SQLite audit only)
- Deployment + Service `k8up-gui` (**ClusterIP port 80 → 8080**)
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
kubectl -n k8up-gui create secret docker-registry ghcr-pull \
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
| Hostname | `k8up-gui.k8up-gui.svc` or `k8up-gui.k8up-gui.svc.cluster.local` |
| Port | **80** |
| Public hostname | e.g. `k8up.your.domain` |
| Auth | Pangolin resource protection only |

## Auth model

**No in-app login.** Access control is Pangolin (SSO/resource rules) + the
in-cluster NetworkPolicy (ingress only from `newt`).

The API never returns 401 for missing identity. Audit rows use
`AUTH_DEFAULT_USER` (default `operator`), or a proxy header if present.

## Verify

```bash
kubectl -n k8up-gui get deploy,svc,pvc
kubectl -n k8up-gui get pods -o wide
kubectl -n newt exec deploy/newt-site-a -- wget -q -O- -T 5 http://k8up-gui.k8up-gui.svc/healthz
kubectl -n k8up-gui port-forward svc/k8up-gui 8080:80
# curl http://127.0.0.1:8080/api/v1/schedules
```

## Migrating off the old PVC-seeded deploy

If you previously used `k8up-gui-app` + seed pod:

```bash
kubectl -n k8up-gui scale deploy/k8up-gui --replicas=0
kubectl apply -k deploy/k8s
# optional cleanup once the new pod is healthy:
kubectl -n k8up-gui delete pvc k8up-gui-app --ignore-not-found
kubectl -n k8up-gui delete pod k8up-gui-seed --ignore-not-found
kubectl -n k8up-gui rollout status deploy/k8up-gui
```

Keep **`k8up-gui-data`** — that is the audit SQLite volume.

## Notes

- RWO data volume → single replica, `Recreate` strategy.
- Image is distroless/static + nonroot (uid 65532); root FS read-only.
- Ephemeral smoke `deploy/ephemeral-smoke.yaml` is obsolete; prefer `deploy/k8s/`.
