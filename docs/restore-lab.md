# Restore Lab — restore testing

Restore testing turns "we take backups" into "we've proven backups restore."
The Restore Lab is an isolated namespace where snapshots are restored and apps
are brought up against the restored data so an operator can look at them —
without touching production, and without the Argo-pause/scale-down choreography
an in-place restore needs.

The design rehearses the real disaster-recovery path: **redeploy from git +
restore data from restic**. A lab run is a dress rehearsal of exactly what a
real recovery would do.

## Two tiers

| Tier | What it does | Per-app setup |
|---|---|---|
| **Data lab** | Restore one snapshot into a scratch PVC in the lab namespace and attach a read-only file-browser pod to eyeball the contents. | none |
| **App lab** | Restore all of an app's PVC snapshots into the lab namespace, deploy the app there from its git source (a cloned Argo Application), replay the SQL dump into the lab database, and reach the running app through the tunnel. | promotion checklist below |

Every lab run proves the restore path end-to-end (PVC provisioning, K8up
Restore CR, restic, credentials) and records the outcome as a `drill` audit
entry — the dashboard shows *last verified restore per app*.

## How an app lab works

1. k8up-btl resolves the app's Argo `Application` (by destination namespace)
   and plans the run: the newest snapshot per source PVC, plus the newest
   `.sql` dump snapshot if the app backs its database up with a
   `k8up.io/backupcommand`.
2. PVCs are pre-created in the lab namespace with their production names and
   specs, then filled by K8up `Restore` CRs whose `backend.s3.bucket` points at
   the **source** namespace's repository (credentials come from the operator's
   global secret — nothing is copied).
3. A clone `Application` (`lab-<namespace>`) is created in the Argo namespace:
   - same git `source` (or the path named by a `k8up-btl.local/lab-path`
     annotation on the source Application — the per-app lab overlay),
   - `spec.source.kustomize.namespace: <lab ns>` rewrites every manifest into
     the lab (this repo's apps hardcode `metadata.namespace`, so
     `destination.namespace` alone would not work),
   - kustomize delete-patches drop the `Namespace` resource and the K8up
     `Schedule` (a cloned Schedule would back lab data up into the production
     repository),
   - `project: restore-lab` — an AppProject whose destinations are locked to
     the lab namespace, with `Schedule` blacklisted, so even a broken clone
     cannot touch anything else,
   - **no automated sync** — synced once at creation, never self-healed,
   - the Argo resources finalizer, so deleting the Application cascades.
4. Once the clone reports Synced + Healthy (recorded as the RTO-actual
   timestamp), the SQL dump — if any — is piped `restic dump | <restore
   command>` into the lab database pod, exactly like a production SQL recovery
   but without quiesce or safety backup (it's a lab).
5. The lab stays up for its TTL (default 24 h, extendable from the UI), then is
   torn down automatically: clone Application deleted (cascade), every PVC in
   the lab namespace deleted, and — because the storage class retains volumes —
   each released PV deleted too. A teardown failure notifies.

Data-tier labs skip steps 1/3/4: one PVC, one Restore CR, then a
`filebrowser` pod mounting the volume read-only.

One lab at a time (mirroring the single-restore rule); a failed lab must be
torn down before the next one starts.

## Isolation (why the lab is safe)

SOPS secrets render into the lab via the normal Argo/KSOPS path, so lab apps
hold **production credentials**. The lab namespace therefore ships with
default-deny **egress** as well as ingress:

- egress allowed: DNS, pods within the lab namespace, and K8up job pods
  (restic needs the S3 endpoint);
- ingress allowed: only from the `newt` (tunnel) namespace;
- anything else — IdP for OIDC token exchange, for example — is an explicit
  rule the consumer adds (see `deploy/k8s/restore-lab/networkpolicy.yaml`).

Same creds, no reachability: apps that call external services run degraded in
the lab, which is the standard enterprise sandbox trade-off.

## Promotion checklist (app lab, one-time per app)

An app is data-tier drillable with zero setup. To promote it to a clickable
app-lab test:

1. **Lab overlay (optional)** — if the app needs lab-specific patches
   (`BASE_URL`, `EXTERNAL_HOST`, …), add e.g. `apps/<name>/lab/` in the GitOps
   repo (`resources: [../manifests]` + patches) and annotate the source
   Application with `k8up-btl.local/lab-path: apps/<name>/lab`. Apps without
   hostname-sensitive config skip this.
2. **IdP redirect URI** — for apps doing native OIDC, add the lab hostname to
   the client's callback URLs (Pocket ID supports wildcards). Forward-auth-only
   apps skip this.
3. **Tunnel hostname** — register `<app>-lab.<domain>` in the reverse proxy
   (Pangolin) pointing at the app's Service in the lab namespace. The resource
   is permanent; it 502s while no lab is up.

## Deploying the lab

The scaffolding is an **optional** kustomize directory,
`deploy/k8s/restore-lab/`: the `restore-lab` Namespace (PSA `baseline` — the
K8up restore pod runs as root there so restored file ownership is preserved,
and baseline-level apps can clone), the `restore-lab` AppProject, the
NetworkPolicies above, and the extra RBAC (PVC/workload create in the lab
namespace, Application create/delete in the Argo namespace, PV delete). Add it
as a second remote base next to `deploy/k8s`.

Backend configuration:

| Env | Default | Meaning |
|---|---|---|
| `RESTORE_LAB_NAMESPACE` | *(empty — feature disabled)* | the lab namespace, e.g. `restore-lab` |
| `RESTORE_LAB_TTL` | `24h` | default lab lifetime before auto-teardown |
| `RESTORE_LAB_APPPROJECT` | `restore-lab` | AppProject assigned to clone Applications |
| `RESTORE_LAB_INSPECT_IMAGE` | `docker.io/filebrowser/filebrowser:v2` | data-tier inspection pod image |

## Limits

- App tier needs a single-source kustomize Application (this repo's layout).
  Multi-source/Helm apps: data tier only.
- Snapshots taken via `backupcommand` (stdin dumps) cannot be restored by a
  `Restore` CR; the lab replays them through the same exec-pipe path as SQL
  recovery. Apps whose *only* backup is a stdin dump get the SQL leg and empty
  PVCs.
- The lab pays the full restic restore cost — there is no mount-from-backup
  shortcut. That is also the point: the copy is the thing being tested.

## Verification tiers (roadmap)

v1 verification is "Argo reports the clone Healthy" (heartbeat) plus the
operator's own eyes. The design leaves room for: scheduled drills (cron over
the same lab flow), sentinel checksums (restic `diff`/`stats` against the
restored volume), and app-level probes. See `docs/IDEAS.md`.
