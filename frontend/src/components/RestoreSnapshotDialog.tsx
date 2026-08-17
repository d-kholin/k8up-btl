import { useEffect, useState } from 'react'
import { api, type K8sObject } from '../api'
import { guessPvc } from '../lib/snapshots'
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
// dashboard activity drill-down). Navigates to /restores on success.
export default function RestoreSnapshotDialog({
  snapshot,
  onClose,
}: {
  snapshot: K8sObject | null
  onClose: () => void
}) {
  const [pvcNamespace, setPvcNamespace] = useState('')
  const [pvcName, setPvcName] = useState('')
  const [confirmText, setConfirmText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!snapshot) return
    setPvcNamespace(snapshot.namespace || '')
    setPvcName(guessPvc(snapshot))
    setConfirmText('')
    setError('')
  }, [snapshot])

  async function submit() {
    if (!snapshot || confirmText !== 'restore') return
    if (!pvcNamespace || !pvcName) {
      setError('PVC namespace and name are required')
      return
    }
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
      window.location.href = '/restores'
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
            onto a PVC. Argo CD reconciliation is paused cluster-wide for the duration, the
            workload is scaled to 0, then brought back.
          </DialogDescription>
        </DialogHeader>

        {error && <Alert variant="danger">{error}</Alert>}

        <div className="grid gap-3 py-2">
          <div className="grid gap-1.5">
            <label className="text-xs text-muted-foreground">PVC namespace</label>
            <Input value={pvcNamespace} onChange={(e) => setPvcNamespace(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <label className="text-xs text-muted-foreground">PVC name</label>
            <Input value={pvcName} onChange={(e) => setPvcName(e.target.value)} />
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
