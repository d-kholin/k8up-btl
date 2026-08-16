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

Identity (optional but preferred):

- Pangolin **Forwarded Headers**: `Remote-User`, `Remote-Email` (enable in Pangolin if available)
- Authentik proxy headers: `X-authentik-username`, `X-authentik-email`

If SSO is enforced only at the Pangolin edge and no identity headers are injected
(common for this lab), the Deployment sets `AUTH_TRUST_EDGE=true` and attributes
actions to `AUTH_DEFAULT_USER` (default `operator`). NetworkPolicy still limits
ingress to the `newt` namespace only.

There is **no** `DEV_AUTH_USER` in the Deployment — unauthenticated requests to `/api/*` return 401 (`/healthz` stays open for probes).

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
