# Future ideas

Backlog of features considered but deliberately not built yet. Move an entry
into the PRD when it's picked up.

## Restore to an alternate PVC (safe restores / restore drills)

_Logged 2026-08-17. Status: **picked up 2026-08-29** as the Restore Lab — see
`docs/restore-lab.md`. What shipped: lab-namespace restores (data tier + full
app-lab clones), TTL teardown, `drill` audit kind, dashboard verification
panel. Still open from the original sketch and the design interview:_

- **Scheduled drills** — cron-style loop running the lab flow unattended
  (Recorder-pattern ticker; the manual flow is the building block).
- **Automated verification tiers** — sentinel checksums via restic
  `diff`/`stats` against the restored volume; app-level probes beyond Argo
  health.
- **Restore-to-alternate-PVC in the source namespace** (the original "copy out
  one table" use case) — the lab covers inspection, but an in-namespace scratch
  restore is still occasionally handy.
- **Pangolin API automation** for lab hostnames (v1 keeps registration manual).

## Coverage-gap detection (not yet picked up)

Cross-reference PVCs against Schedules and surface "these PVCs are not backed
up" plus schedules whose recent runs never produced a snapshot.
