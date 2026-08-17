# Future ideas

Backlog of features considered but deliberately not built yet. Move an entry
into the PRD when it's picked up.

## Restore to an alternate PVC (safe restores / restore drills)

_Logged 2026-08-17. Status: liked, deferred._

Today restores target the original PVC, which means downtime (Argo pause +
scale-down) and overwriting live data. K8up's `Restore` CR supports restoring
into a different claim, which unlocks:

- **Safe recovery**: restore a snapshot into a new/scratch PVC without touching
  the running workload — inspect or copy out what you need (e.g. one database
  table) and delete it.
- **No orchestration needed**: nothing mounts the target PVC, so the whole
  Argo-pause / scale-down / scale-up choreography is skipped.
- **Restore drills**: periodically restore a recent snapshot to a scratch PVC,
  verify contents (checksum/marker file), delete, and record the result in the
  audit log — turning "we take backups" into "we've proven backups restore."

Sketch: add a target-PVC choice to `RestoreSnapshotDialog` (original vs
new/other claim); orchestrator gets a "detached" mode that creates the Restore
CR with the alternate `claimName` and skips Argo/scale steps; drills would be a
cron-style loop in the backend plus a freshness-style panel on the dashboard.

## Coverage-gap detection (not yet picked up)

Cross-reference PVCs against Schedules and surface "these PVCs are not backed
up" plus schedules whose recent runs never produced a snapshot.
