import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type K8sObject, type RecoveryPlan } from '../api'
import { formatWhen } from '../lib/utils'
import { Alert } from './ui/alert'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './ui/dialog'
import { Input } from './ui/input'

// Point-in-time recovery dialog for SQL dump snapshots: pipes the dump into
// the DB pod's git-defined restore command, quiesces only that app's
// workloads, and optionally restores sibling PVCs to the same point in time.

// PVC snapshots further than this from the dump are unticked by default.
const AUTO_SELECT_WINDOW_SEC = 6 * 3600

export default function RecoveryDialog({
  snapshot,
  onClose,
}: {
  snapshot: K8sObject | null
  onClose: () => void
}) {
  const navigate = useNavigate()
  const [plan, setPlan] = useState<RecoveryPlan | null>(null)
  const [planError, setPlanError] = useState('')
  const [dbPod, setDbPod] = useState('')
  const [checkedPvcs, setCheckedPvcs] = useState<Record<string, boolean>>({})
  const [safetyBackup, setSafetyBackup] = useState(true)
  const [confirmText, setConfirmText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!snapshot) return
    setPlan(null)
    setPlanError('')
    setError('')
    setConfirmText('')
    setSafetyBackup(true)
    api
      .recoveryPlan(snapshot.namespace || '', snapshot.name || '')
      .then((p) => {
        setPlan(p)
        setDbPod(p.bestGuess || p.dbCandidates[0]?.podName || '')
        const init: Record<string, boolean> = {}
        for (const o of p.pvcOptions) {
          init[o.pvcName] = Math.abs(o.deltaSeconds) <= AUTO_SELECT_WINDOW_SEC
        }
        setCheckedPvcs(init)
      })
      .catch((e: Error) => setPlanError(e.message))
  }, [snapshot])

  const db = useMemo(
    () => plan?.dbCandidates.find((c) => c.podName === dbPod) || null,
    [plan, dbPod],
  )

  async function submit() {
    if (!snapshot || !plan || !db || confirmText !== 'restore') return
    setBusy(true)
    setError('')
    try {
      await api.startRecovery({
        namespace: snapshot.namespace || '',
        dumpSnapshotName: snapshot.name || '',
        dbPodName: db.podName,
        skipSafetyBackup: !safetyBackup,
        pvcRestores: plan.pvcOptions
          .filter((o) => checkedPvcs[o.pvcName])
          .map((o) => ({ snapshotName: o.snapshotName, pvcName: o.pvcName })),
      })
      onClose()
      navigate('/restores')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const canStart = !!db?.hasRestoreCommand && confirmText === 'restore' && !busy

  return (
    <Dialog open={!!snapshot} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Point-in-time recovery (SQL dump)</DialogTitle>
          <DialogDescription>
            Pipes snapshot{' '}
            <span className="font-mono text-foreground">
              {snapshot?.namespace}/{snapshot?.name}
            </span>{' '}
            into the database, stopping only this app's workloads while it runs. Argo CD is
            paused cluster-wide for the duration.
          </DialogDescription>
        </DialogHeader>

        {error && <Alert variant="danger">{error}</Alert>}
        {planError && <Alert variant="danger">{planError}</Alert>}
        {!plan && !planError && <div className="py-4 text-sm text-muted-foreground">Loading plan…</div>}

        {plan && (
          <div className="grid gap-4 py-2">
            {plan.dbCandidates.length === 0 && (
              <Alert variant="danger">
                No running pod in this namespace has the{' '}
                <code className="font-mono">k8up.io/backupcommand</code> annotation — cannot
                locate the database this dump came from.
              </Alert>
            )}

            {plan.dbCandidates.length > 1 && (
              <div className="grid gap-1.5">
                <label className="text-xs text-muted-foreground">Database pod</label>
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 font-mono text-sm"
                  value={dbPod}
                  onChange={(e) => setDbPod(e.target.value)}
                >
                  {plan.dbCandidates.map((c) => (
                    <option key={c.podName} value={c.podName}>
                      {c.podName}
                      {c.podName === plan.bestGuess ? ' (matched dump filename)' : ''}
                    </option>
                  ))}
                </select>
              </div>
            )}

            {db && (
              <>
                <div className="grid gap-1.5">
                  <label className="text-xs text-muted-foreground">
                    Restore command
                    {db.commandSource ? (
                      <span className="ml-1 opacity-70">— source: {db.commandSource}</span>
                    ) : null}
                  </label>
                  {db.hasRestoreCommand ? (
                    <>
                      <div className="rounded-md border bg-muted/40 px-3 py-2 font-mono text-xs">
                        {db.podName} ({db.container}): sh -c "{db.restoreCommand}"
                      </div>
                      {db.commandSource?.startsWith('derived') && (
                        <p className="text-[11px] text-muted-foreground">
                          Derived from the pod's k8up.io/backupcommand (dump swapped for the
                          client, env assignments kept). Pin a different command with the{' '}
                          <code className="font-mono">k8up-btl.local/restore-command</code>{' '}
                          annotation.
                        </p>
                      )}
                    </>
                  ) : (
                    <Alert variant="danger">
                      <div className="space-y-1 text-xs">
                        <p>{db.commandError || 'No restore command could be resolved.'}</p>
                        <p>
                          Add <code className="font-mono">k8up-btl.local/restore-command</code>{' '}
                          to the workload's pod template in git.
                        </p>
                      </div>
                    </Alert>
                  )}
                </div>

                <div className="grid gap-1.5">
                  <label className="text-xs text-muted-foreground">
                    Workloads stopped during recovery
                    {db.quiesceGrouping?.startsWith('argo-app:') ? (
                      <span className="ml-1 opacity-70">
                        — same Argo app ({db.quiesceGrouping.slice('argo-app:'.length)})
                      </span>
                    ) : db.quiesceGrouping === 'annotation' ? (
                      <span className="ml-1 opacity-70">— from quiesce-workloads annotation</span>
                    ) : db.quiesceGrouping === 'namespace' ? (
                      <span className="ml-1 opacity-70">— whole namespace</span>
                    ) : null}
                  </label>
                  {db.quiesceWarning && <Alert variant="warning">{db.quiesceWarning}</Alert>}
                  {db.workloadsToStop.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {db.workloadsToStop.map((w) => (
                        <Badge key={`${w.kind}/${w.name}`} variant="secondary">
                          {w.kind.toLowerCase()}/{w.name}
                        </Badge>
                      ))}
                    </div>
                  )}
                  <p className="text-[11px] text-muted-foreground">
                    The DB workload
                    {db.workload ? ` (${db.workload.kind.toLowerCase()}/${db.workload.name})` : ''} stays
                    up to receive the dump. Nothing outside this list is touched.
                  </p>
                </div>
              </>
            )}

            {plan.pvcOptions.length > 0 && (
              <div className="grid gap-1.5">
                <label className="text-xs text-muted-foreground">
                  Also restore PVCs to the same point in time
                </label>
                <div className="grid gap-1 rounded-md border p-2">
                  {plan.pvcOptions.map((o) => (
                    <label key={o.pvcName} className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={!!checkedPvcs[o.pvcName]}
                        onChange={(e) =>
                          setCheckedPvcs((prev) => ({ ...prev, [o.pvcName]: e.target.checked }))
                        }
                      />
                      <span className="font-mono text-xs">{o.pvcName}</span>
                      <span className="ml-auto text-xs text-muted-foreground">
                        {formatWhen(o.date)} · {formatDelta(o.deltaSeconds)}
                      </span>
                    </label>
                  ))}
                </div>
                <p className="text-[11px] text-muted-foreground">
                  Nearest snapshot per PVC; entries more than 6h from the dump start unticked.
                </p>
              </div>
            )}

            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={safetyBackup}
                onChange={(e) => setSafetyBackup(e.target.checked)}
              />
              Take a fresh safety backup before touching data (recommended)
            </label>

            <div className="grid gap-1.5">
              <label className="text-xs text-muted-foreground">
                Type <span className="font-mono text-foreground">restore</span> to confirm
              </label>
              <Input
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder="restore"
                autoComplete="off"
              />
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={!canStart} onClick={submit}>
            {busy ? 'Starting…' : 'Start recovery'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function formatDelta(seconds: number): string {
  const abs = Math.abs(seconds)
  const dir = seconds <= 0 ? 'before dump' : 'after dump'
  if (abs < 90) return `${abs}s ${dir}`
  if (abs < 5400) return `${Math.round(abs / 60)}m ${dir}`
  if (abs < 172800) return `${Math.round(abs / 3600)}h ${dir}`
  return `${Math.round(abs / 86400)}d ${dir}`
}
