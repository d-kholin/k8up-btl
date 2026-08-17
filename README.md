# k8up btl (ketchup bottle)

Web GUI for [K8up](https://k8up.io/) backup visibility and **Argo CD-aware** one-click restores.
Formerly referred to as k8up-gui; product name is **k8up btl**. Cluster resources use the `k8up-btl` name.

**Status:** greenfield scaffold (v0). Core restore orchestration, API surface, UI shell, and in-cluster manifests are in place; live cluster wiring and Prometheus metric names still need validation (see open questions in the PRD).

## Why

Restoring a PVC with K8up on an Argo CD–managed cluster is a multi-step dance (pause sync → scale down → Restore CR → scale up → resume sync). This app automates that flow server-side so an operator never touches Argo or kubectl for routine restores.

## Features (PRD v1)

| Area | Capability |
|------|------------|
| Visibility | Cluster-wide Schedules, Snapshots, live Backup/Restore/Check/Prune status |
| Restore | One-click restore with Argo pause/resume, durable mid-restore state |
| Browse | Per-snapshot file tree + file/folder/snapshot download via `restic` (read-only) |
| Compare | Diff two snapshots (`restic diff`): added/removed/modified files with byte deltas |
| Notify | ntfy + email alerts for job failures, restore outcomes, interrupted restores |
| Actions | Ad-hoc Backup / Check CRs |
| Audit | 90-day restore + download history |
| Auth | No in-app auth; Pangolin + Newt NetworkPolicy only |

Full product requirements: [`docs/PRD.md`](docs/PRD.md).

## Architecture

```
Browser (SPA) ──REST/SSE──▶ Go backend ──client-go──▶ K8s API
                                │                      ├─ K8up CRDs
                                │                      ├─ Deploy/STS + Pods/PVCs
                                │                      ├─ Argo Application CRDs
                                │                      └─ Secrets (restic creds)
                                └──restic subprocess──▶ S3/Garage repos
```

- All Kubernetes/Argo calls are **server-side only**.
- Restore CRs are one-shot and must **not** receive Argo tracking labels.
- Argo pause state is persisted on the Application annotation  
  `restore-gui.local/paused-state` so a backend crash never silently leaves sync paused.

## Repo layout

```
backend/          Go API + restore orchestrator + restic runner
frontend/         React (Vite + TypeScript) SPA
deploy/k8s/       Deployment, Service, SA, ClusterRole/Binding
docs/PRD.md       Product requirements
Dockerfile        Multi-stage backend+frontend+restic image
```

## Quick start (dev)

### Prerequisites

- Go 1.24+
- Node 20+
- Optional: `kubectl` + kubeconfig for live cluster mode
- Optional: `restic` on `PATH` for browse/download

### Backend

```bash
cd backend
cp ../.env.example .env   # edit as needed
go run ./cmd/server
# listens on :8080
```

### Frontend

```bash
cd frontend
npm install
npm run dev
# proxies /api to :8080
```

### Config (env)

| Variable | Default | Meaning |
|----------|---------|---------|
| `HTTP_ADDR` | `:8080` | Listen address |
| `KUBECONFIG` | (in-cluster) | Out-of-cluster kubeconfig path |
| `ARGOCD_NAMESPACE` | `argocd` | Where Application CRs live |
| `AUDIT_DB_PATH` | `/data/audit.db` | SQLite audit log path |
| `PROMETHEUS_URL` | _(empty)_ | Optional Prometheus base URL |
| `GRAFANA_DASHBOARD_URL` | _(empty)_ | Link/embed target for K8up dashboard |
| `AUTH_USER_HEADER` | `X-authentik-username` | Forward-auth identity header |
| `AUTH_EMAIL_HEADER` | `X-authentik-email` | Forward-auth email header |
| `DEV_AUTH_USER` | _(empty)_ | If set, trust this user when headers absent (local only) |
| `STATIC_DIR` | _(empty)_ | If set, serve SPA from this directory |
| `NTFY_TOPIC` | _(empty)_ | Enables ntfy notifications (job failures, restore outcomes) |
| `NTFY_URL` | `https://ntfy.sh` | ntfy server base URL |
| `NTFY_TOKEN` | _(empty)_ | Optional ntfy access token (Bearer) |
| `SMTP_HOST` / `SMTP_FROM` / `SMTP_TO` | _(empty)_ | Set all three to enable email notifications |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_TLS` | `starttls` | `starttls` \| `tls` (implicit, 465) \| `none` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | _(empty)_ | Optional SMTP auth |
| `NOTIFY_RESTORE_SUCCESS` | `true` | Also notify on successful GUI restores |

With any channel configured, the backend alerts on: failed Backup/Check/Prune/Restore jobs (detected by the history sweeper, restart-safe), GUI restore completion/failure (including "Argo still paused" attention states), and interrupted restores found at startup. Verify delivery with `curl -X POST .../api/v1/notify/test`.

## Deploy (cluster)

Published image: **`ghcr.io/d-kholin/k8up-btl`** (multi-arch amd64/arm64 via GitHub Actions).

Manifests under `deploy/k8s/` assume:

- Single cluster, cluster-scoped RBAC for K8up CRDs
- Argo CD Applications in `argocd`
- PVC for SQLite audit DB only (`k8up-btl-data`)
- Nodes can pull the GHCR image (public package, or pull secret if private)

```bash
kubectl apply -k deploy/k8s
kubectl -n k8up-btl rollout status deploy/k8up-btl
```

Wire the Service behind Pangolin/Newt (ClusterIP `k8up-btl.k8up-btl.svc:80`). Full steps, pin tags/digests, and migration off the old PVC-seed path: [`docs/deploy.md`](docs/deploy.md).

Do **not** GitOps-track transient Restore CRs this app creates.

## Security notes

- Browser never receives SA tokens or restic passwords.
- Browse/download path only allows read-only restic commands (`ls`, `dump`, `stats`).
- Backend holds standing Secret read access (same secrets K8up uses) — acceptable for single-operator behind SSO; documented tradeoff in PRD §5.3.
- Destructive actions require UI confirmation; backend still validates request shape.

## Development status

Scaffold implements:

- [x] Project layout, PRD, Makefile, Dockerfile, K8s manifests
- [x] Go HTTP API surface (schedules, snapshots, jobs, restore, browse, audit, SSE)
- [x] Restore orchestrator with try/finally Argo resume + annotation durable state
- [x] Startup interrupted-restore scanner
- [x] SQLite audit log + 90-day prune hook
- [x] restic runner (read-only allowlist)
- [x] React SPA shell (routes for all primary views)
- [ ] Live validation of K8up/Prometheus metric names on cluster
- [ ] E2E restore against real Argo-managed workload
- [ ] Forward-auth header names confirmed against Authentik/Traefik

## License

Private / homelab — adjust before publishing.
