import { useEffect, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { api } from '../api'
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

export type NamespaceOption = { namespace: string; hasSchedule: boolean }

/** Confirm-and-fire dialog for on-demand Backup / Check CRs. The created spec
 * inherits backend + pod security from the namespace's Schedule server-side. */
export default function RunBackupDialog({
  open,
  onOpenChange,
  options,
  initialNamespace,
  onCreated,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  options: NamespaceOption[]
  initialNamespace?: string
  onCreated?: (kind: 'backup' | 'check', namespace: string, name: string) => void
}) {
  const [ns, setNs] = useState('')
  const [busy, setBusy] = useState<'backup' | 'check' | ''>('')
  const [error, setError] = useState('')

  // Reset only when the dialog opens — options refresh on a poll interval and
  // must not clobber an in-progress selection.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (open) {
      setNs(initialNamespace || options[0]?.namespace || '')
      setError('')
      setBusy('')
    }
  }, [open])

  const selected = options.find((o) => o.namespace === ns)

  async function run(kind: 'backup' | 'check') {
    if (!ns) {
      setError('Select a namespace first')
      return
    }
    setBusy(kind)
    setError('')
    try {
      const obj = kind === 'backup' ? await api.createBackup(ns) : await api.createCheck(ns)
      onCreated?.(kind, ns, obj.name || '')
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Run on-demand job</DialogTitle>
          <DialogDescription>
            Creates a one-shot K8up Backup or Check CR. Repository, pod config and pod security are
            inherited from the namespace&apos;s Schedule.
          </DialogDescription>
        </DialogHeader>
        {error && <Alert variant="danger">{error}</Alert>}
        <div className="space-y-2">
          <label htmlFor="run-job-ns" className="text-sm font-medium">
            Namespace
          </label>
          <select
            id="run-job-ns"
            value={ns}
            onChange={(e) => setNs(e.target.value)}
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {options.length === 0 && <option value="">No namespaces found</option>}
            {options.map((o) => (
              <option key={o.namespace} value={o.namespace}>
                {o.namespace}
                {o.hasSchedule ? '' : ' (no schedule)'}
              </option>
            ))}
          </select>
        </div>
        {selected && !selected.hasSchedule && (
          <Alert variant="warning">
            <span className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>
                <span className="font-mono text-xs">{selected.namespace}</span> has no backup
                Schedule — there is nothing to inherit the repository or pod security from, so this
                job relies on K8up&apos;s global credentials and operator defaults, and may fail.
              </span>
            </span>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="secondary" disabled={busy !== '' || !ns} onClick={() => run('check')}>
            {busy === 'check' ? 'Creating…' : 'Run check'}
          </Button>
          <Button disabled={busy !== '' || !ns} onClick={() => run('backup')}>
            {busy === 'backup' ? 'Creating…' : 'Run backup'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
