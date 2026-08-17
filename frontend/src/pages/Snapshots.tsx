import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ChevronDown, ChevronRight, FolderSearch, GitCompareArrows } from 'lucide-react'
import { api, type K8sObject } from '../api'
import { formatWhen } from '../lib/utils'
import { isSqlDump, snapSpec, snapTime, workloadFromPaths } from '../lib/snapshots'
import RestoreSnapshotDialog from '../components/RestoreSnapshotDialog'
import RecoveryDialog from '../components/RecoveryDialog'
import SnapshotCalendar, { dayKey } from '../components/SnapshotCalendar'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader } from '../components/ui/card'
import { Input } from '../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

type NsGroup = { namespace: string; items: K8sObject[] }

export default function Snapshots() {
  // Deep links from the dashboard land here as /snapshots?namespace=<ns>.
  const [searchParams] = useSearchParams()
  const nsParam = searchParams.get('namespace') || ''
  const [items, setItems] = useState<K8sObject[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState(nsParam)
  const [selectedDay, setSelectedDay] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Record<string, boolean>>(
    nsParam ? { [nsParam]: true } : {},
  )

  // Restore dialogs (PVC restore vs SQL dump recovery)
  const [restoreSnap, setRestoreSnap] = useState<K8sObject | null>(null)
  const [recoverSnap, setRecoverSnap] = useState<K8sObject | null>(null)

  useEffect(() => {
    api.snapshots().then(setItems).catch((e: Error) => setError(e.message))
  }, [])

  // Snapshots matching the text filter — the calendar shades these, so it
  // reflects e.g. a namespace search.
  const textFiltered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return items.filter((s) => {
      if (!q) return true
      const spec = snapSpec(s)
      const hay = [s.namespace, s.name, ...(spec.paths || []), spec.id, spec.repository]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [items, filter])

  const groups = useMemo(() => {
    const filtered = textFiltered.filter((s) => {
      if (!selectedDay) return true
      const t = snapTime(s)
      return t > 0 && dayKey(new Date(t)) === selectedDay
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
  }, [textFiltered, selectedDay])

  // Picking a day narrows the list to a handful — open everything.
  function selectDay(day: string | null) {
    setSelectedDay(day)
    if (day) {
      const next: Record<string, boolean> = {}
      for (const s of textFiltered) {
        const t = snapTime(s)
        if (t > 0 && dayKey(new Date(t)) === day) next[s.namespace || 'default'] = true
      }
      setExpanded(next)
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
            {selectedDay ? ` · on ${selectedDay}` : ''}
          </span>
          {selectedDay && (
            <Badge variant="secondary" className="cursor-pointer" onClick={() => selectDay(null)}>
              {selectedDay} ✕
            </Badge>
          )}
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
        <CardContent className="border-t pt-4">
          <div className="flex flex-wrap items-start gap-6">
            <SnapshotCalendar snapshots={textFiltered} selected={selectedDay} onSelect={selectDay} />
            <div className="min-w-[200px] flex-1 text-sm text-muted-foreground">
              <p>
                Pick a day to jump to that day's restore points — shading follows the text filter
                above. Days are shown in your local timezone.
              </p>
              {selectedDay && total === 0 && (
                <p className="mt-2">No snapshots on {selectedDay} match the current filter.</p>
              )}
            </div>
          </div>
        </CardContent>
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
                                <Link
                                  to={`/snapshots/${s.namespace}/${s.name}/diff`}
                                  title="Compare with a previous snapshot"
                                >
                                  <GitCompareArrows className="h-3.5 w-3.5" />
                                  Compare
                                </Link>
                              </Button>
                              <Button asChild size="sm" variant="ghost">
                                <Link to={`/snapshots/${s.namespace}/${s.name}/browse`}>
                                  <FolderSearch className="h-3.5 w-3.5" />
                                  Browse
                                </Link>
                              </Button>
                              {isSqlDump(s) ? (
                                <Button size="sm" onClick={() => setRecoverSnap(s)}>
                                  Recover…
                                </Button>
                              ) : (
                                <Button size="sm" onClick={() => setRestoreSnap(s)}>
                                  Restore…
                                </Button>
                              )}
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

      <RestoreSnapshotDialog snapshot={restoreSnap} onClose={() => setRestoreSnap(null)} />
      <RecoveryDialog snapshot={recoverSnap} onClose={() => setRecoverSnap(null)} />
    </div>
  )
}
