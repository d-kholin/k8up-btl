# AGENTS.md — k8up-btl

## Purpose

K8up backup/restore GUI with Argo CD-aware restore orchestration. Spec: `docs/PRD.md`.

## Stack

- **Backend:** Go, `client-go` + dynamic client for CRDs (K8up + Argo Application)
- **Frontend:** React + TypeScript + Vite
- **Data:** SQLite audit log; durable restore state on Argo **application-controller** STS annotation
- **Binary dep:** `restic` in PATH / container image (read-only: `ls`, `dump`, `stats`)

## Hard rules (do not violate)

1. **Never** apply Argo tracking labels to Restore/Backup/Check CRs created by this app.
2. **Argo pause = global:** scale `argocd/argocd-application-controller` StatefulSet to **0** for the whole restore; always scale it back in `defer` (success or failure).
3. Persist restore progress on STS annotation `restore-gui.local/paused-state` (JSON).
4. No mutating restic commands from browse/download.
5. No k8s/Argo credentials or restic secrets to the browser.
6. No in-app login; proxy + NetworkPolicy only.
7. SQL dump recovery only execs git-sourced commands — the
   `k8up-btl.local/restore-command` annotation (pod or owner workload),
   `SQL_RESTORE_CMD_*` env config, or a derivation of the pod's own
   `k8up.io/backupcommand`. Never a command from the request body. Quiesce
   scope: annotation list → same Argo app (instance label / tracking-id) →
   whole namespace as last resort, always excluding the DB workload and always
   shown verbatim in the confirm dialog.
8. **In-place** restore targets are locked to what the snapshot backed up
   (source PVC from `spec.paths`); the server rejects any other target. The
   only exception is the Restore Lab (`docs/restore-lab.md`), whose restores
   land exclusively in the configured lab namespace (`RESTORE_LAB_NAMESPACE`),
   never in a source namespace.
9. Restore Lab rules: lab resources (PVCs, Restore CRs, inspection workloads)
   are created ONLY in the lab namespace; clone Applications are created ONLY
   with `project: restore-lab` (destination-locked AppProject), with NO
   automated sync, and with kustomize delete-patches for the `Namespace`
   resource and the K8up `Schedule` (a cloned Schedule would back lab data up
   into the production repository). Teardown owns deleting every PVC in the
   lab namespace and their retained PVs. No Argo pause is ever taken for a
   lab run, and labs never quiesce or scale production workloads.

## Why global pause

Child Applications are git-managed by the root app-of-apps with `automated.selfHeal`. Clearing per-app `syncPolicy.automated` is immediately healed. Stopping the application-controller freezes all reconciliation until resume.

## Annotation contract (on application-controller STS)

```
restore-gui.local/paused-state = {
  "restoreId": "...",
  "argoControllerReplicas": 1,
  "argoPausedGlobally": true,
  "workload": {"kind":"Deployment","namespace":"...","name":"..."},
  "originalReplicas": 1,
  "step": "pausing_argo|scaling_down|restoring|scaling_up|resuming_argo|done|failed",
  ...
}
```

## Prefer

- Small focused packages under `backend/internal/`
- Fail start if controller already paused / annotation present
