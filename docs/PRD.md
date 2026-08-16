# PRD: K8up Backup/Restore GUI with Argo CD-Aware Restore

## 1. Problem

K8up (Kubernetes backup operator, restic-based) has no first-party GUI. Backup status is visible via `kubectl`/Prometheus; restores are a manual multi-step process: scale down the consuming workload, apply a `Restore` CR, wait, scale back up. On a cluster managed by Argo CD, this manual process additionally fights Argo's self-heal, which will silently revert the replica-count scale-down mid-restore if not paused first.

## 2. Goal

A web GUI that lets a user view backup history/status and perform a one-click restore. When the target workload is Argo CD-managed, the system automatically pauses and resumes Argo sync around the restore — the user never touches Argo directly.

## 3. Non-goals (v1)

- Multi-cluster support (single cluster only)
- Multi-tenant / per-user RBAC scoping (assume a single trusted operator or small team with equal access)
- Selective file-level *restore into the cluster* (i.e. writing individual files back into a live PVC) — K8up's `Restore` CR only supports whole-snapshot restore, so this stays out of scope for v1. Browse and download of individual files is in scope (see 5.3) as a workaround for this gap.
- Editing Schedule/backup configuration (creation of schedules stays in git/YAML, out of scope)
- Building the GUI's own authentication system (see 7.4 — auth is delegated to existing reverse-proxy/SSO)

## 4. Users

Single operator (homelab / small team), technically fluent, comfortable with Kubernetes concepts but wants to avoid manual kubectl choreography for restores.

## 5. Functional requirements

### 5.1 Backup visibility
- List all `Schedule` objects cluster-wide (all namespaces watched — no namespace allowlist in v1), with next-run time and last status.
- List `Snapshot` CRs (mirrors restic snapshots) per PVC/namespace: timestamp, size, associated `Backup`/`Check` status.
- Show live status of in-progress `Backup`/`Restore`/`Check`/`Prune` jobs (via k8s watch, not polling, where practical).
- **In v1**: query Prometheus for backup success/failure history and surface it alongside the CR-derived status (assumes a reachable Prometheus endpoint with K8up's metrics already scraped — see open questions for exact metric names/endpoint config).

### 5.2 Restore (core feature)
Trigger from GUI: user selects a Snapshot → clicks Restore → confirms target PVC/namespace.

Backend orchestration, fully automatic, no manual steps required from the user:

1. **Resolve ownership**: given the target PVC, find the owning Deployment or StatefulSet (support both as scale-down targets), then determine if it's Argo CD-managed (check for `app.kubernetes.io/instance` label or `argocd.argoproj.io/tracking-id` annotation on the resource). Record the Argo `Application` name if found.
2. **If Argo-managed**: read the `Application`'s current `spec.syncPolicy`, persist it (see 5.4 — durable state), then patch the `Application` to remove/null `spec.syncPolicy.automated` (pause sync). Do not touch other syncPolicy fields (`syncOptions`, etc.) — preserve them for restoration.
3. **Scale down**: patch the Deployment/StatefulSet to 0 replicas. Poll until pods are actually terminated (not just patch-and-proceed) — restic needs the PVC unmounted.
4. **Restore**: create a K8up `Restore` CR pointing at the selected snapshot. Poll its status to completion or failure. Do **not** apply Argo tracking labels to this object — it's a one-shot resource, not something Argo should ever manage or prune.
5. **Scale up**: patch the workload back to its original replica count (captured in step 3).
6. **Resume sync**: patch the Argo `Application`'s `syncPolicy` back to the value persisted in step 2.
7. **Report final state to the user**: success, or explicit failure with which step failed and current Argo sync state (paused vs resumed) — see 5.4.

### 5.3 File-level browse and download
Since K8up has no API for inspecting snapshot contents and `Restore` only supports whole-PVC restore, this is implemented by talking to restic directly against the repository — independent of K8up's CRDs.

- User selects a Snapshot in the GUI and can browse its file/directory tree.
- User can download an individual file from within a snapshot without restoring anything into the cluster.
- Implementation: this is read-only against the S3/Garage-backed restic repository and never touches a PVC, so — unlike the restore flow — it does **not** need to run inside a per-request Job/Pod. The backend can invoke `restic` directly as a subprocess against the repository over the network, using repo credentials fetched from the same Secret K8up itself uses for that PVC (`RESTIC_PASSWORD`, S3 credentials, etc. passed as env vars per-request, not persisted to disk). `restic ls <snapshot>` for the tree listing, `restic dump <snapshot> <path>` streamed directly to the HTTP response for a single-file download.
- Large files/directory trees: stream rather than buffer fully in memory; `restic dump` output can be piped directly to the HTTP response.
- This is read-only against the repository — no restic command that mutates the repo (no `restic restore`, no `forget`/`prune` triggered from this path) should be reachable from the browse/download feature.
- Tradeoff to be aware of: since the backend process itself holds repo credentials (fetched per-request, but the backend has standing access to the Secrets), it has broader access to decrypt any repo it can reach compared to a model where credentials are scoped to an ephemeral, isolated Job per request. Acceptable for this deployment's threat model (single-operator homelab behind Authentik) but worth documenting as a conscious tradeoff.

### 5.4 Non-Argo-managed workloads
If the target workload has no Argo ownership markers, skip steps 2 and 6 entirely — just scale down / restore / scale up.

### 5.5 Failure handling and durable state (critical)
- Sync-policy restoration (step 6) must run regardless of whether steps 3–5 succeed, time out, or error. Implement as try/finally or equivalent — a failure here must never leave Argo silently stuck in manual/paused mode.
- Persist restore-in-progress state (target Application name, original syncPolicy, original replica count, current step) somewhere durable — not just backend process memory. An annotation on the Argo `Application` itself (e.g. `restore-gui.local/paused-state: <json>`) is acceptable since it travels with the object and survives a backend restart.
- On backend startup, check for any Application carrying an in-progress restore annotation and surface it to the user as "restore was interrupted, sync currently paused" rather than leaving it silently unresolved.
- GUI must clearly distinguish: "restore complete, sync resumed" vs "restore failed, sync resumed" vs "restore failed, sync still paused — needs attention."

### 5.6 Trigger ad-hoc actions
- Trigger an on-demand `Backup` or `Check` from the GUI (creates the corresponding CR).
- No rate-limiting/cooldown required — a confirmation dialog before firing is sufficient protection against accidental repeats.

### 5.7 Restore history / audit log
- Retain restore history for 90 days (time-based retention, not count-based). Older entries can be pruned/archived by the backend on a schedule.
- File downloads (5.3) should also be logged (who/when/which file/which snapshot) in the same audit trail, even though they don't touch the cluster state.

### 5.8 Dashboards
- **Success/failure history**: time-series view of Backup/Check/Prune job outcomes over time, per Schedule and cluster-wide. K8up ships an official Grafana dashboard (bundled in its Helm chart) — evaluate reusing/embedding that rather than rebuilding it, since it already covers this. If building natively into this GUI instead of embedding Grafana, source this from the CR status history (5.1) plus Prometheus if available.
- **Bytes backed up / dedup stats / bytes recovered**: K8up's own Prometheus/pushgateway metric names for these are not reliably documented as of this writing (K8up's own docs list operator and restic-pushgateway metrics as missing documentation) — do not assume specific metric names without verifying against the live cluster's actual exposed metrics first (see open questions).
  - As a more reliable source, capture this directly from restic itself, since the backend already invokes `restic` for file browse/download (5.3):
    - Per-backup: parse `restic backup --json` summary output for `total_bytes_processed` (logical bytes backed up) and `data_added` (actual new/unique bytes written post-dedup) — this requires K8up's backup job itself to be invoked with `--json` and that output captured, or for this GUI to independently query `restic stats` after the fact.
    - Per-snapshot: `restic stats <snapshot-id> --mode restore-size` (logical/recoverable size) vs `restic stats --mode raw-data` (actual repo storage consumed) to compute dedup ratio over time.
    - Restore: restic has no built-in restore-size stats output; "bytes recovered" for a given restore should be computed as the restore-size of the snapshot that was restored (captured at restore time, from 5.2).
  - Dashboard should show: bytes backed up over time (per schedule and aggregate), cumulative dedup ratio/storage saved, bytes recovered per restore event (from the audit log in 5.7).

- Backend must not expose any service account or Argo API token to the browser — all k8s/Argo API calls happen server-side.
- Live status updates via k8s `watch` API preferred over polling, to keep the UI responsive during long-running restore jobs.
- All destructive actions (restore, scale-down) require explicit confirmation in the UI.

## 7. Architecture

### 7.1 Backend
- Language/framework: suggest Go using `client-go`/`controller-runtime` (matches K8up's own stack and simplifies working with its CRD types), but implementation language is the coding agent's/user's choice.
- Exposes REST (or GraphQL) API + a watch/streaming endpoint (SSE or WebSocket) to the frontend for live status.
- Runs with a Kubernetes ServiceAccount — see RBAC below. No separate Argo CD API client is required: `Application` is itself a CRD (`argoproj.io/v1alpha1`), so it's managed through the same k8s client.
- Requires the `restic` binary available in the backend's container image, and network access to the S3/Garage backend(s) K8up uses. For file browse/download (5.3), the backend invokes `restic` as a direct subprocess against the repository — no Job/Pod spawning or exec needed, since this path is read-only and never touches a PVC. Repo credentials are fetched from the relevant Secret per-request and passed as environment variables to the subprocess, not written to disk.

### 7.2 RBAC (ServiceAccount permissions needed)
- `get`, `list`, `watch` on K8up CRDs (`schedules`, `backups`, `restores`, `checks`, `prunes`, `snapshots`, `archives`, `prebackuppods`) — cluster-scoped or namespace-scoped per deployment choice.
- `create` on `restores.k8up.io`, `backups.k8up.io`, `checks.k8up.io`.
- `get`, `list`, `patch` on `deployments`/`statefulsets` (for scaling) in target namespaces.
- `get`, `patch` on `applications.argoproj.io` in the Argo CD namespace (commonly `argocd`).
- `get`, `list` on `pods` (to poll termination during scale-down) and `persistentvolumeclaims` (for ownership resolution).
- `get` on the `Secret`(s) holding restic repository credentials (same secrets K8up itself uses), scoped to whatever namespaces hold backup targets — needed for file browse/download.

### 7.3 Frontend
- SPA (React or similar), talks only to the backend API — never directly to the k8s or Argo API.
- Views: backup/schedule dashboard, snapshot list with restore action, snapshot file browser with download, in-progress job status, restore history/audit log (90-day retention).

### 7.4 Deployment
- Runs in-cluster as a standard Deployment (ServiceAccount + in-cluster kubeconfig, no external kubeconfig/token management needed).
- Exposed via the existing Pangolin/Traefik reverse proxy, consistent with other self-hosted services (Vaultwarden, Immich, etc.).
- Auth is delegated entirely to the existing Authentik setup (e.g. via forward-auth at the Pangolin/Traefik layer) — the GUI itself implements no login system. Backend should trust the auth headers/identity Traefik forwards rather than re-implementing session handling.
- Manifests should be committed to git and deployed via Argo CD like the rest of the cluster (with the caveat from section 5.2 step 4 — its own `Restore` CRs must never carry Argo tracking labels, but the GUI's *own* Deployment/manifests are normal git-managed Argo-deployed resources like anything else).

## 8. Open questions for implementation

- Exact Prometheus endpoint/query setup: is Prometheus already reachable in-cluster (e.g. via existing Grafana/Prometheus monitoring stack), and what metric names does the installed K8up version expose? Needs confirming against the live cluster before implementation. This includes checking whether K8up's exposed metrics already include byte-count/data-added fields (undocumented as of this writing) before building a redundant restic-based stats collector — if they're already there and reliable, prefer them over re-deriving from restic directly.
- Forward-auth integration details: which header(s) Authentik/Traefik forward-auth populates for identity, and whether any identity distinction is needed at all given this is single-operator/small-team with equal access (per non-goals, no per-user RBAC in v1).
- Concurrent repo access: confirm restic's locking behavior is acceptable when a browse/download read happens while a scheduled Backup or Prune is running against the same repo (reads typically don't block on a non-exclusive lock, but worth a smoke test against the actual restic version in use, since Prune takes an exclusive lock and could transiently block a browse request).
- Success/failure dashboard: embed/link K8up's existing official Grafana dashboard vs. build an equivalent view natively in this GUI. Embedding is less work but means the user leaves the GUI (or it's iframed) for that view; native means duplicating a dashboard K8up already maintains. Recommend embedding/linking for v1 given it already exists and is maintained upstream, reserving native dashboard work for the byte/dedup stats K8up doesn't already provide.

## 9. Success criteria

- User can view backup/snapshot status for all watched namespaces without kubectl.
- User can restore a snapshot for an Argo-managed workload with a single click and confirmation, with zero manual Argo interaction.
- A killed/crashed backend mid-restore never leaves an Argo Application permanently and silently stuck in paused sync — this state is always either auto-recovered or clearly surfaced.
