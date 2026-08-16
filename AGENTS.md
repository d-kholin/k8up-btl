# AGENTS.md — k8up-gui

## Purpose

K8up backup/restore GUI with Argo CD-aware restore orchestration. Spec: `docs/PRD.md`.

## Stack

- **Backend:** Go, `client-go` + dynamic client for CRDs (K8up + Argo Application)
- **Frontend:** React + TypeScript + Vite
- **Data:** SQLite audit log; durable restore pause state on Argo Application annotation
- **Binary dep:** `restic` in PATH / container image (read-only: `ls`, `dump`, `stats`)

## Hard rules (do not violate)

1. **Never** apply Argo tracking labels (`app.kubernetes.io/instance`, `argocd.argoproj.io/instance`, tracking-id) to Restore/Backup/Check CRs created by this app.
2. **Always** resume Argo `syncPolicy.automated` in a `defer`/finally path after pause — success or failure.
3. Persist restore progress on annotation `restore-gui.local/paused-state` (JSON), not only in memory.
4. No mutating restic commands from browse/download (`restore`, `forget`, `prune`, `backup` forbidden).
5. No k8s/Argo credentials or restic secrets to the browser — server-side only.
6. Auth is reverse-proxy identity headers only; no local password system in v1.

## Annotation contract

```
restore-gui.local/paused-state = {
  "restoreId": "...",
  "application": {"namespace":"argocd","name":"..."},
  "originalSyncPolicy": { ... },   // full previous automated block or null
  "workload": {"kind":"Deployment|StatefulSet","namespace":"...","name":"..."},
  "originalReplicas": 1,
  "step": "pausing_argo|scaling_down|restoring|scaling_up|resuming_argo|done|failed",
  "snapshotId": "...",
  "pvc": {"namespace":"...","name":"..."},
  "startedAt": "RFC3339",
  "lastError": ""
}
```

## API sketch

- `GET /api/health`
- `GET /api/v1/me`
- `GET /api/v1/schedules`
- `GET /api/v1/snapshots`
- `GET /api/v1/jobs`
- `POST /api/v1/restores` — start restore
- `GET /api/v1/restores` / `GET /api/v1/restores/{id}`
- `POST /api/v1/restores/{id}/resume-argo` — manual unstick
- `GET /api/v1/snapshots/{ns}/{name}/files?path=`
- `GET /api/v1/snapshots/{ns}/{name}/download?path=`
- `POST /api/v1/backups` / `POST /api/v1/checks`
- `GET /api/v1/audit`
- `GET /api/v1/events` — SSE
- `GET /api/v1/interrupted` — apps with stale pause annotation

## Prefer

- Small focused packages under `backend/internal/`
- Unstructured/dynamic client for CRDs unless typed deps are lightweight
- Streaming HTTP for `restic dump`
- Confirmation only in UI; backend still validates

## Out of scope (v1)

Multi-cluster, multi-tenant RBAC, schedule editing, file-level in-cluster restore, app-native login.
