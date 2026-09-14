import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type K8sObject, type RestorePlan } from '../api'
import { formatWhen } from '../lib/utils'
import { sourcePvcCandidates } from '../lib/snapshots'
import { Alert } from './ui/alert'
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

// Shared type-to-confirm restore dialog (used by the Snapshots page and the
// dashboard activity drill-down). The restore target is locked to the PVC the
// snapshot was taken from — the backend enforces the same rule — so a snapshot
// can never be restored onto a different volume by retyping the target.
export default function RestoreSnapshotDialog({
  snapshot,
  onClose,
}: {
  snapshot: K8sObject | null
  onClose: () => void
}) {
  const navigate = useNavigate()
  const [pvcName, setPvcName] = useState('')
  const [confirmText, setConfirmText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [plan, setPlan] = useState<RestorePlan | null>(null)
  const [planError, setPlanError] = useState('')

  const candidates = useMemo(() => (snapshot ? sourcePvcCandidates(snapshot) : []), [snapshot])
  const pvcNamespace = snapshot?.namespace || ''
  useEffect(() => {
    setPlan(null)
    setPlanError('')
    if (!snapshot || !pvcName) return
    let cancelled = false
    api
      .restorePlan(snapshot.namespace || '', snapshot.name || '', pvcName)
      .then((value) => {
        if (!cancelled) setPlan(value)
      })
      .catch((e: Error) => {
        if (!cancelled) setPlanError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [snapshot, pvcName])

  useEffect(() => {
    if (!snapshot) return
    setPvcName(sourcePvcCandidates(snapshot)[0] || '')
    setConfirmText('')
    setError('')
  }, [snapshot])

  async function submit() {
    if (
      !snapshot ||
      confirmText !== 'restore' ||
      !pvcName ||
      !plan ||
      plan.pvcName !== pvcName ||
      plan.snapshotName !== snapshot.name ||
      plan.namespace !== snapshot.namespace
    )
      return
    setBusy(true)
    setError('')
    try {
      const result = await api.startRestore({
        snapshotNamespace: snapshot.namespace || '',
        snapshotName: snapshot.name || '',
        pvcNamespace,
        pvcName,
      })
      onClose()
      navigate('/restores?id=' + encodeURIComponent(result.restoreId))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!snapshot} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore production volume</DialogTitle>
          <DialogDescription>
            Restores snapshot{' '}
            <span className="font-mono text-foreground">
              {snapshot?.namespace}/{snapshot?.name}
            </span>{' '}
            back onto the PVC it was taken from. Argo CD reconciliation is paused cluster-wide for
            the duration, the workload is scaled to 0, then brought back.
          </DialogDescription>
        </DialogHeader>

        {error && <Alert variant="danger">{error}</Alert>}
        {planError && <Alert variant="danger">Cannot prepare restore: {planError}</Alert>}
        {!plan && !planError && !!pvcName && (
          <p role="status" className="text-sm text-muted-foreground">
            Checking snapshot and affected workload…
          </p>
        )}
        {plan && plan.pvcName === pvcName && (
          <div className="space-y-3 rounded-md border bg-muted/40 p-4 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">Recovery point</p>
              <p className="mt-1 font-medium">{formatWhen(plan.date)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Workload stopped during restore</p>
              <p className="mt-1 break-all font-medium">
                {plan.workload.kind} {plan.workload.namespace}/{plan.workload.name}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {plan.originalReplicas} replicas → 0 → {plan.originalReplicas} replicas
              </p>
            </div>
            <p className="text-sm">
              Existing volume data will be overwritten. Argo CD reconciliation is paused
              cluster-wide while the restore runs.
            </p>
            <p className="text-xs text-muted-foreground">
              This preview reflects current ownership. The server resolves it again when the restore
              starts.
            </p>
          </div>
        )}
        {candidates.length === 0 && (
          <Alert variant="danger">
            This snapshot has no restorable PVC source (e.g. an application-level dump). It cannot
            be restored onto a volume from here.
          </Alert>
        )}

        <div className="grid gap-3 py-2">
          <div className="grid gap-1.5">
            <label htmlFor="restore-pvc" className="text-xs text-muted-foreground">
              Restore target (locked to source)
            </label>
            {candidates.length > 1 ? (
              <select
                id="restore-pvc"
                className="h-9 rounded-md border border-input bg-background px-3 font-mono text-sm"
                value={pvcName}
                onChange={(e) => setPvcName(e.target.value)}
              >
                {candidates.map((c) => (
                  <option key={c} value={c}>
                    {pvcNamespace}/{c}
                  </option>
                ))}
              </select>
            ) : (
              <div
                id="restore-pvc"
                className="flex min-h-9 items-center break-all rounded-md border border-input bg-muted/40 px-3 py-2 font-mono text-sm"
              >
                {candidates.length === 1 ? `${pvcNamespace}/${candidates[0]}` : '—'}
              </div>
            )}
            <p className="text-[11px] text-muted-foreground">
              Snapshots can only be restored to the PVC they backed up — the server rejects any
              other target.
            </p>
          </div>
          <div className="grid gap-1.5">
            <label htmlFor="restore-confirm" className="text-xs text-muted-foreground">
              Type <span className="font-mono text-foreground">restore</span> to confirm
            </label>
            <Input
              id="restore-confirm"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder="restore"
              autoComplete="off"
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={
              busy ||
              confirmText !== 'restore' ||
              !pvcNamespace ||
              !pvcName ||
              !plan ||
              plan.pvcName !== pvcName ||
              plan.snapshotName !== snapshot?.name ||
              plan.namespace !== snapshot?.namespace
            }
            onClick={submit}
          >
            {busy ? 'Starting…' : 'Start restore'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
