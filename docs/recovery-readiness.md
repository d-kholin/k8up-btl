# Recovery readiness UI

The dashboard prioritizes per-resource backup coverage, interrupted/active restores,
and operator-recorded restore verification. Historical backup activity and storage
statistics are available under **Activity & storage**. Namespace schedules and job
controls remain in Workloads; audit records remain in Audit.

## Coverage semantics

- Each PVC and each observed SQL dump path has independent snapshot evidence.
  A recent snapshot of one resource does not establish coverage for its siblings.
- PVCs explicitly annotated `k8up.io/backup: "false"` are listed as excluded.
- Current means the most recent dated snapshot is within 1.5 times the namespace's
  single, predictable backup cadence. Complex cron expressions, multiple schedules,
  and future timestamps are classified as unknown rather than assumed current.
- Missing means there is no dated snapshot matching the source. This reports
  available cluster snapshot metadata, not a new integrity check of the repository.
- SQL dumps that have never produced a snapshot cannot be discovered from this
  inventory. A dump does not establish volume-level coverage for its database PVC.
- If PVC inventory fails, the UI marks the inventory partial. Failed schedule or
  snapshot requests suppress the dashboard coverage assessment until refreshed.
- The configured Restore Lab namespace is excluded from dashboard production coverage.
- Restore verification requires an operator's passed verdict. Unavailable verification
  is shown as unavailable, never as evidence that no test has happened.

## Navigation and restore workflow

Snapshot namespace, search, and date filters; workload searches; restore selection and
search; and audit filters/pagination are kept in the URL for reloads, bookmarks, and
browser back/forward navigation.

PVC confirmation uses a read-only `GET /api/v1/snapshots/{namespace}/{name}/restore-plan?pvc=...`
preview of the source volume, snapshot date, owning workload, and replica count. An
unavailable preview disables submission. The existing restore endpoint still validates
the source and resolves ownership again when starting; this preview is not a lock.

Restore operations show named resources and a stage timeline above retained/live logs.
Failed operations do not invent a last completed stage. Argo resume is only reported
confirmed when the backend explicitly reports it. Cancellation and lab teardown use
the shared confirmation dialog. Production orchestration and lab isolation rules are
unchanged.

## Validation

Run `npm test`, `npm run typecheck`, and `npm run build` from `frontend`.
The coverage tests exercise missing siblings, explicit exclusions, independent SQL
dumps, ambiguous schedules, invalid/future dates, and supported cron intervals.

Run `go test ./internal/api ./internal/k8s ./internal/restore` from `backend`.
The restore-plan test verifies source locking, affected workload/replica reporting,
and that all preview Kubernetes actions are read-only.
