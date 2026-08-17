import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ChevronDown, ChevronRight, FolderSearch } from 'lucide-react'
import { api, type K8sObject } from '../api'
import { formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader } from '../components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog'
import { Input } from '../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

type SnapSpec = { date?: string; id?: string; paths?: string[]; repository?: string }

function snapSpec(s: K8sObject): SnapSpec {
  return (s.spec || {}) as SnapSpec
}

function snapTime(s: K8sObject): number {
  const d = snapSpec(s).date || s.creationTimestamp
  return d ? new Date(d).getTime() : 0
}

function workloadFromPaths(paths: string[]): string {
  if (!paths.length) return '(unknown)'
  return paths
    .map((p) => p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || p)
    .join(', ')
}

function guessPvc(s: K8sObject): string {
  const paths = snapSpec(s).paths || []
  return (
    paths
      .map((p) => p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || '')
      .find((b) => b && !b.endsWith('.sql')) || ''
  )
}

type NsGroup = { namespace: string; items: K8sObject[] }

export default function Snapshots() {
  // Deep links from the dashboard land here as /snapshots?namespace=<ns>.
  const [searchParams] = useSearchParams()
  const nsParam = searchParams.get('namespace') || ''
  const [items, setItems] = useState<K8sObject[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState(nsParam)
  const [expanded, setExpanded] = useState<Record<string, boolean>>(
    nsParam ? { [nsParam]: true } : {},
  )
  const [busy, setBusy] = useState(false)

  // Restore dialog
  const [restoreSnap, setRestoreSnap] = useState<K8sObject | null>(null)
  const [pvcNamespace, setPvcNamespace] = useState('')
  const [pvcName, setPvcName] = useState('')
  const [confirmText, setConfirmText] = useState('')

  useEffect(() => {
    api.snapshots().then(setItems).catch((e: Error) => setError(e.message))
  }, [])

  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const filtered = items.filter((s) => {
      if (!q) return true
      const spec = snapSpec(s)
      const hay = [s.namespace, s.name, ...(spec.paths || []), spec.id, spec.repository]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })

    const map = new Map<string, K8sObject[]>()
    for (const s of filtered) {
      const ns = s.namespace || 'default'
      if (!map.has(ns)) map.set(ns, [])
      map.get(ns)!.push(s)
    }
    const out: NsGroup[] = [...map.entries()].map(([namespace, list]) => ({
      namespace,
      items: list.sort((a, b) => snapTime(b) - snapTime(a)),
    }))
    out.sort((a, b) => a.namespace.localeCompare(b.namespace))
    return out
  }, [items, filter])

  function openRestore(s: K8sObject) {
    setRestoreSnap(s)
    setPvcNamespace(s.namespace || '')
    setPvcName(guessPvc(s))
    setConfirmText('')
  }

  async function submitRestore() {
    if (!restoreSnap) return
    if (confirmText !== 'restore') return
    if (!pvcNamespace || !pvcName) {
      setError('PVC namespace and name are required')
      return
    }
    setBusy(true)
    setError('')
    try {
      await api.startRestore({
        snapshotNamespace: restoreSnap.namespace || '',
        snapshotName: restoreSnap.name || '',
        pvcNamespace,
        pvcName,
      })
      setRestoreSnap(null)
      window.location.href = '/restores'
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const total = groups.reduce((n, g) => n + g.items.length, 0)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Snapshots</h1>
        <p className="text-sm text-muted-foreground">Grouped by namespace · collapsed by default</p>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center gap-3 space-y-0">
          <Input
            placeholder="Filter namespace, PVC, path…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="max-w-sm"
          />
          <span className="text-sm text-muted-foreground">
            {groups.length} namespaces · {total} snapshots
          </span>
          <div className="ml-auto flex gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                const next: Record<string, boolean> = {}
                for (const g of groups) next[g.namespace] = true
                setExpanded(next)
              }}
            >
              Expand all
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => setExpanded({})}>
              Collapse all
            </Button>
          </div>
        </CardHeader>
      </Card>

      {groups.length === 0 && (
        <Card>
          <CardContent className="py-8 text-sm text-muted-foreground">No snapshots match.</CardContent>
        </Card>
      )}

      {groups.map((g) => {
        const open = !!expanded[g.namespace]
        const latest = g.items[0]
        return (
          <Card key={g.namespace}>
            <button
              type="button"
              className="flex w-full items-center gap-3 px-5 py-4 text-left transition-colors hover:bg-row-hover"
              onClick={() => setExpanded((e) => ({ ...e, [g.namespace]: !open }))}
            >
              {open ? (
                <ChevronDown className="h-4 w-4 text-muted-foreground" />
              ) : (
                <ChevronRight className="h-4 w-4 text-muted-foreground" />
              )}
              <div className="min-w-0 flex-1">
                <div className="font-mono text-sm font-medium">{g.namespace}</div>
                <div className="truncate text-xs text-muted-foreground">
                  latest {formatWhen(snapSpec(latest).date || latest.creationTimestamp)}
                  {snapSpec(latest).paths?.[0] ? ` · ${workloadFromPaths(snapSpec(latest).paths || [])}` : ''}
                </div>
              </div>
              <Badge variant="secondary">{g.items.length}</Badge>
            </button>

            {open && (
              <CardContent className="border-t pt-4">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>When</TableHead>
                      <TableHead>Snapshot</TableHead>
                      <TableHead>Workload / paths</TableHead>
                      <TableHead className="text-right">Actions</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {g.items.map((s) => {
                      const spec = snapSpec(s)
                      return (
                        <TableRow key={`${s.namespace}/${s.name}`}>
                          <TableCell className="whitespace-nowrap text-muted-foreground">
                            {formatWhen(spec.date || s.creationTimestamp)}
                          </TableCell>
                          <TableCell>
                            <div className="font-mono text-xs">{s.name}</div>
                            {spec.id && (
                              <div className="font-mono text-[11px] text-muted-foreground" title={spec.id}>
                                {spec.id.slice(0, 12)}…
                              </div>
                            )}
                          </TableCell>
                          <TableCell className="max-w-[240px] truncate font-mono text-xs text-muted-foreground">
                            {workloadFromPaths(spec.paths || [])}
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex justify-end gap-2">
                              <Button asChild size="sm" variant="ghost">
                                <Link to={`/snapshots/${s.namespace}/${s.name}/browse`}>
                                  <FolderSearch className="h-3.5 w-3.5" />
                                  Browse
                                </Link>
                              </Button>
                              <Button size="sm" onClick={() => openRestore(s)}>
                                Restore…
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </CardContent>
            )}
          </Card>
        )
      })}

      <Dialog open={!!restoreSnap} onOpenChange={(o) => !o && setRestoreSnap(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm restore</DialogTitle>
            <DialogDescription>
              Restores snapshot{' '}
              <span className="font-mono text-foreground">
                {restoreSnap?.namespace}/{restoreSnap?.name}
              </span>{' '}
              onto a PVC. Argo CD reconciliation is paused cluster-wide for the duration, the
              workload is scaled to 0, then brought back.
            </DialogDescription>
          </DialogHeader>

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
            <Button variant="outline" onClick={() => setRestoreSnap(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={busy || confirmText !== 'restore' || !pvcNamespace || !pvcName}
              onClick={submitRestore}
            >
              {busy ? 'Starting…' : 'Start restore'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
