# Deploying k8up-gui (in-cluster)

## Steady-state resources

```bash
kubectl apply -k deploy/k8s
```

Creates:

- Namespace `k8up-gui`
- SA + ClusterRole/Binding (K8up CRDs, scale workloads, Argo Applications, secrets)
- PVCs: `k8up-gui-app` (binaries), `k8up-gui-data` (SQLite audit)
- Deployment + Service `k8up-gui` (**ClusterIP port 80 → 8080**)
- NetworkPolicies: default-deny ingress, allow from `newt` only

No private image registry yet: runtime image is public `debian:bookworm-slim`; the Go binary, `restic`, SPA, and CA bundle live on **`k8up-gui-app` PVC**.

## Seed / upgrade the app PVC

```bash
# build bundle on a machine with Go + npm
make build && (cd frontend && npm ci && npm run build)
# pack: server, restic, static/, ca-certificates.crt, entrypoint.sh → app.tgz

kubectl -n k8up-gui scale deploy/k8up-gui --replicas=0
kubectl apply -f deploy/k8s/seed-pod.yaml
kubectl -n k8up-gui wait --for=condition=Ready pod/k8up-gui-seed --timeout=120s
kubectl -n k8up-gui cp ./app.tgz k8up-gui-seed:/tmp/app.tgz
kubectl -n k8up-gui exec k8up-gui-seed -- sh -c \
  'rm -rf /app/*; tar -xzf /tmp/app.tgz -C /app && chmod -R a+rX /app && chown -R 65532:65532 /app'
kubectl -n k8up-gui delete pod k8up-gui-seed
kubectl -n k8up-gui scale deploy/k8up-gui --replicas=1
kubectl -n k8up-gui rollout status deploy/k8up-gui
```

## Pangolin (Newt) resource

Point an HTTP resource at the Kubernetes Service (not a pod IP):

| Field | Value |
|-------|--------|
| Method / scheme | **http** |
| Hostname | `k8up-gui.k8up-gui.svc` or `k8up-gui.k8up-gui.svc.cluster.local` |
| Port | **80** |
| Public hostname | e.g. `k8up.your.domain` |
| Auth | Authentik / forward-auth (same as other apps) |

## Auth model

**No in-app login.** Access control is Pangolin (SSO/resource rules) + the
in-cluster NetworkPolicy (ingress only from `newt`).

The API never returns 401 for missing identity. Audit rows use
`AUTH_DEFAULT_USER` (default `operator`), or a proxy header if one happens
to be present (`Remote-User`, etc.) — headers are optional enrichment only.

## Verify

```bash
kubectl -n k8up-gui get deploy,svc,pvc
kubectl -n newt exec deploy/newt-site-a -- wget -q -O- -T 5 http://k8up-gui.k8up-gui.svc/healthz
kubectl -n k8up-gui port-forward svc/k8up-gui 8080:80
# curl -H 'X-authentik-username: you' http://127.0.0.1:8080/api/v1/schedules
```

## Notes

- RWO volumes → single replica, `Recreate` strategy.
- Later: replace PVC-bin approach with a real image on GHCR and GitOps app in `k3s-hl`.
- Ephemeral smoke manifest `deploy/ephemeral-smoke.yaml` is obsolete; prefer `deploy/k8s/`.
