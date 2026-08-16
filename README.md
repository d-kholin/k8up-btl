# k8up-gui

Web GUI for [K8up](https://k8up.io/) backup visibility and **Argo CD-aware** one-click restores.

**Status:** greenfield scaffold (v0). Core restore orchestration, API surface, UI shell, and in-cluster manifests are in place; live cluster wiring and Prometheus metric names still need validation (see open questions in the PRD).

## Why

Restoring a PVC with K8up on an Argo CD–managed cluster is a multi-step dance (pause sync → scale down → Restore CR → scale up → resume sync). This app automates that flow server-side so an operator never touches Argo or kubectl for routine restores.

## Features (PRD v1)

| Area | Capability |
|------|------------|
| Visibility | Cluster-wide Schedules, Snapshots, live Backup/Restore/Check/Prune status |
| Restore | One-click restore with Argo pause/resume, durable mid-restore state |
| Browse | Per-snapshot file tree + single-file download via `restic` (read-only) |
| Actions | Ad-hoc Backup / Check CRs |
| Audit | 90-day restore + download history |
| Auth | Delegated to reverse-proxy / Authentik forward-auth (no app login) |

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

## Deploy (cluster)

Manifests under `deploy/k8s/` assume:

- Single cluster, cluster-scoped RBAC for K8up CRDs
- Argo CD Applications in `argocd`
- PVC for SQLite audit DB
- Image built from root `Dockerfile` (includes `restic`)

Wire the Service behind Pangolin/Traefik + Authentik forward-auth like other homelab apps. Commit manifests to your GitOps repo (or point Argo at this repo) — **not** the transient Restore CRs this app creates.

```bash
kubectl apply -k deploy/k8s
```

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
