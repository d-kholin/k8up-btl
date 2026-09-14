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
import PageHeader from '../components/PageHeader'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../components/ui/table'

type NsGroup = { namespace: string; items: K8sObject[] }

export default function Snapshots() {
  // Deep links from the dashboard land here as /snapshots?namespace=<ns>.
  const [searchParams, setSearchParams] = useSearchParams()
  const nsParam = searchParams.get('namespace') || ''
  const [items, setItems] = useState<K8sObject[]>([])
  const [error, setError] = useState('')
  const filter = searchParams.get('q') || ''
  const selectedDay = searchParams.get('day')
  const [loading, setLoading] = useState(true)
  const [calendarOpen, setCalendarOpen] = useState(!!selectedDay)
  const [limits, setLimits] = useState<Record<string, number>>({})
  const setParam = (key: string, value: string) =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  const load = () => {
    setLoading(true)
    api
      .snapshots()
      .then((value) => {
        setItems(value)
        setError('')
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }
  const [expanded, setExpanded] = useState<Record<string, boolean>>(
    nsParam ? { [nsParam]: true } : {},
  )

  // Restore dialogs (PVC restore vs SQL dump recovery)
  const [restoreSnap, setRestoreSnap] = useState<K8sObject | null>(null)
  const [recoverSnap, setRecoverSnap] = useState<K8sObject | null>(null)

  useEffect(() => {
    load()
  }, [])

  // Snapshots matching the text filter — the calendar shades these, so it
  // reflects e.g. a namespace search.
  const textFiltered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return items.filter((s) => {
      if (nsParam && s.namespace !== nsParam) return false
      if (!q) return true
      const spec = snapSpec(s)
      const hay = [s.namespace, s.name, ...(spec.paths || []), spec.id, spec.repository]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [items, filter, nsParam])

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
    out.sort((a, b) => snapTime(b.items[0]) - snapTime(a.items[0]))
    return out
  }, [textFiltered, selectedDay])

  // Picking a day narrows the list to a handful — open everything.
  function selectDay(day: string | null) {
    setParam('day', day || '')
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
      <PageHeader
        title="Snapshots"
        description="Find a recovery point, inspect its files, or compare changes before restoring."
        actions={
          <Button variant="outline" disabled={loading} onClick={load}>
            Refresh
          </Button>
        }
      />

      {error && <Alert variant="danger">{error}</Alert>}

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center gap-3 space-y-0">
          <select
            aria-label="Namespace"
            className="h-9 max-w-full rounded-md border border-input bg-background px-3 text-sm"
            value={nsParam}
            onChange={(e) => setParam('namespace', e.target.value)}
          >
            <option value="">All namespaces</option>
            {[
              ...new Set([
                ...items.map((s) => s.namespace || 'default'),
                ...(nsParam ? [nsParam] : []),
              ]),
            ]
              .sort()
              .map((ns) => (
                <option key={ns}>{ns}</option>
              ))}
          </select>
          <Input
            aria-label="Search snapshot sources"
            placeholder="Search volume, dump, or snapshot…"
            value={filter}
            onChange={(e) => setParam('q', e.target.value)}
            className="max-w-sm"
          />
          <span className="text-sm text-muted-foreground">
            {groups.length} namespaces · {total} snapshots
            {selectedDay ? ` · on ${selectedDay}` : ''}
          </span>
          {selectedDay && (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => selectDay(null)}
              aria-label="Clear date filter"
            >
              {selectedDay} ✕
            </Button>
          )}
          <div className="ml-auto flex gap-2">
            <Button
              variant="outline"
              size="sm"
              aria-expanded={calendarOpen}
              onClick={() => setCalendarOpen((v) => !v)}
            >
              Date filter
            </Button>
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
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() =>
                setExpanded(Object.fromEntries(groups.map((g) => [g.namespace, false])))
              }
            >
              Collapse all
            </Button>
          </div>
        </CardHeader>
        {calendarOpen && (
          <CardContent className="border-t pt-4">
            <div className="flex flex-wrap items-start gap-6">
              <SnapshotCalendar
                snapshots={textFiltered}
                selected={selectedDay}
                onSelect={selectDay}
              />
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
        )}
      </Card>

      {loading && (
        <p role="status" className="py-8 text-sm text-muted-foreground">
          Loading restore points…
        </p>
      )}
      {!loading && !error && groups.length === 0 && (
        <Card>
          <CardContent className="space-y-3 py-8 text-sm text-muted-foreground">
            <p>
              {items.length
                ? 'No snapshots match these filters.'
                : 'No snapshots found. Run a backup from Workloads to create a recovery point.'}
            </p>
            {items.length ? (
              <Button variant="outline" onClick={() => setSearchParams({})}>
                Clear filters
              </Button>
            ) : (
              <Button asChild variant="outline">
                <Link to="/workloads">View workloads</Link>
              </Button>
            )}
          </CardContent>
        </Card>
      )}

      {groups.map((g) => {
        const open = expanded[g.namespace] !== false
        const latest = g.items[0]
        return (
          <Card key={g.namespace}>
            <button
              type="button"
              aria-expanded={open}
              className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-row-hover sm:px-5 sm:py-4"
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
                  {snapSpec(latest).paths?.[0]
                    ? ` · ${workloadFromPaths(snapSpec(latest).paths || [])}`
                    : ''}
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
                      <TableHead className="hidden md:table-cell">Workload / paths</TableHead>
                      <TableHead className="text-right">Actions</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {g.items.slice(0, limits[g.namespace] || 10).map((s) => {
                      const spec = snapSpec(s)
                      return (
                        <TableRow key={`${s.namespace}/${s.name}`}>
                          <TableCell className="whitespace-nowrap text-muted-foreground">
                            {formatWhen(spec.date || s.creationTimestamp)}
                          </TableCell>
                          <TableCell>
                            <div className="font-mono text-xs">{s.name}</div>
                            {spec.id && (
                              <div
                                className="font-mono text-[11px] text-muted-foreground"
                                title={spec.id}
                              >
                                {spec.id.slice(0, 12)}…
                              </div>
                            )}
                          </TableCell>
                          <TableCell className="hidden max-w-[240px] truncate font-mono text-xs text-muted-foreground md:table-cell">
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
                                  <span className="hidden sm:inline">Compare</span>
                                </Link>
                              </Button>
                              <Button asChild size="sm" variant="ghost">
                                <Link
                                  to={`/snapshots/${s.namespace}/${s.name}/browse`}
                                  title="Browse files"
                                >
                                  <FolderSearch className="h-3.5 w-3.5" />
                                  <span className="hidden sm:inline">Browse</span>
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
                {g.items.length > (limits[g.namespace] || 10) && (
                  <Button
                    className="mt-3"
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      setLimits((prev) => ({
                        ...prev,
                        [g.namespace]: (prev[g.namespace] || 10) + 25,
                      }))
                    }
                  >
                    Show more · {g.items.length - (limits[g.namespace] || 10)} remaining
                  </Button>
                )}
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
