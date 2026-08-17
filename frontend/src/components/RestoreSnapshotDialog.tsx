import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type K8sObject } from '../api'
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

  const candidates = useMemo(
    () => (snapshot ? sourcePvcCandidates(snapshot) : []),
    [snapshot],
  )
  const pvcNamespace = snapshot?.namespace || ''

  useEffect(() => {
    if (!snapshot) return
    setPvcName(sourcePvcCandidates(snapshot)[0] || '')
    setConfirmText('')
    setError('')
  }, [snapshot])

  async function submit() {
    if (!snapshot || confirmText !== 'restore' || !pvcName) return
    setBusy(true)
    setError('')
    try {
      await api.startRestore({
        snapshotNamespace: snapshot.namespace || '',
        snapshotName: snapshot.name || '',
        pvcNamespace,
        pvcName,
      })
      onClose()
      navigate('/restores')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!snapshot} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Confirm restore</DialogTitle>
          <DialogDescription>
            Restores snapshot{' '}
            <span className="font-mono text-foreground">
              {snapshot?.namespace}/{snapshot?.name}
            </span>{' '}
            back onto the PVC it was taken from. Argo CD reconciliation is paused cluster-wide
            for the duration, the workload is scaled to 0, then brought back.
          </DialogDescription>
        </DialogHeader>

        {error && <Alert variant="danger">{error}</Alert>}
        {candidates.length === 0 && (
          <Alert variant="danger">
            This snapshot has no restorable PVC source (e.g. an application-level dump). It
            cannot be restored onto a volume from here.
          </Alert>
        )}

        <div className="grid gap-3 py-2">
          <div className="grid gap-1.5">
            <label className="text-xs text-muted-foreground">Restore target (locked to source)</label>
            {candidates.length > 1 ? (
              <select
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
              <div className="flex h-9 items-center rounded-md border border-input bg-muted/40 px-3 font-mono text-sm">
                {candidates.length === 1 ? `${pvcNamespace}/${candidates[0]}` : '—'}
              </div>
            )}
            <p className="text-[11px] text-muted-foreground">
              Snapshots can only be restored to the PVC they backed up — the server rejects any
              other target.
            </p>
          </div>
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

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={busy || confirmText !== 'restore' || !pvcNamespace || !pvcName}
            onClick={submit}
          >
            {busy ? 'Starting…' : 'Start restore'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
