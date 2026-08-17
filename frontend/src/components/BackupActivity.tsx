import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { FolderSearch } from 'lucide-react'
import type { BackupEvent, K8sObject } from '../api'
import { cn } from '../lib/utils'
import { isSqlDump, snapSpec, workloadFromPaths } from '../lib/snapshots'
import RestoreSnapshotDialog from './RestoreSnapshotDialog'
import RecoveryDialog from './RecoveryDialog'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

type DayAgg = {
  succeeded: number
  failed: number
  running: number
  unknown: number
  events: BackupEvent[]
}

type KindFilter = 'Backup' | 'Check' | 'Prune' | 'Restore' | 'all'

const WEEKS = 53
const CELL = 11
const GAP = 3
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function cellColor(a?: DayAgg): string {
  if (!a) return 'var(--activity-empty)'
  if (a.failed > 0) return a.succeeded > 0 ? 'var(--activity-mixed)' : 'var(--activity-failed)'
  if (a.succeeded > 0) {
    if (a.succeeded >= 10) return 'var(--activity-l4)'
    if (a.succeeded >= 5) return 'var(--activity-l3)'
    if (a.succeeded >= 2) return 'var(--activity-l2)'
    return 'var(--activity-l1)'
  }
  if (a.running > 0) return 'var(--activity-running)'
  return 'var(--activity-empty)'
}

// matchSnapshots finds the Snapshot CR(s) a successful Backup run produced:
// same namespace, snapshot date inside the run window (with slack for clock
// skew and for restic finishing just after the CR flips to finished).
export function matchSnapshots(e: BackupEvent, snapshots: K8sObject[]): K8sObject[] {
  if (e.kind !== 'Backup' || e.status !== 'succeeded') return []
  const started = new Date(e.startedAt).getTime()
  const start = started - 2 * 60_000
  const end = (e.finishedAt ? new Date(e.finishedAt).getTime() : started + 30 * 60_000) + 5 * 60_000
  return snapshots.filter((s) => {
    if (s.namespace !== e.namespace) return false
    const d = snapSpec(s).date || s.creationTimestamp
    if (!d) return false
    const t = new Date(d).getTime()
    return t >= start && t <= end
  })
}

function summarize(a?: DayAgg): string {
  if (!a) return 'no runs recorded'
  const parts: string[] = []
  if (a.succeeded) parts.push(`${a.succeeded} succeeded`)
  if (a.failed) parts.push(`${a.failed} failed`)
  if (a.running) parts.push(`${a.running} running`)
  if (a.unknown) parts.push(`${a.unknown} unknown`)
  return parts.join(' · ') || 'no runs recorded'
}

export default function BackupActivity({
  events,
  snapshots,
  loading,
}: {
  events: BackupEvent[]
  snapshots: K8sObject[]
  loading: boolean
}) {
  const [kind, setKind] = useState<KindFilter>('Backup')
  const [selected, setSelected] = useState<string | null>(null)
  const [restoreSnap, setRestoreSnap] = useState<K8sObject | null>(null)
  const [recoverSnap, setRecoverSnap] = useState<K8sObject | null>(null)
  const [hover, setHover] = useState<{ key: string; label: string; x: number; y: number } | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Grid: 53 week columns (Sun–Sat rows) ending in the current week.
  const { weeks, todayKey } = useMemo(() => {
    const today = new Date()
    today.setHours(0, 0, 0, 0)
    const start = new Date(today)
    start.setDate(start.getDate() - start.getDay() - (WEEKS - 1) * 7)
    const weeks: Date[][] = []
    for (let w = 0; w < WEEKS; w++) {
      const col: Date[] = []
      for (let d = 0; d < 7; d++) {
        const day = new Date(start)
        day.setDate(start.getDate() + w * 7 + d)
        col.push(day)
      }
      weeks.push(col)
    }
    return { weeks, todayKey: dayKey(today) }
  }, [])

  const filtered = useMemo(
    () => (kind === 'all' ? events : events.filter((e) => e.kind === kind)),
    [events, kind],
  )

  const byDay = useMemo(() => {
    const m = new Map<string, DayAgg>()
    for (const e of filtered) {
      const k = dayKey(new Date(e.startedAt))
      let a = m.get(k)
      if (!a) {
        a = { succeeded: 0, failed: 0, running: 0, unknown: 0, events: [] }
        m.set(k, a)
      }
      a[e.status] += 1
      a.events.push(e)
    }
    return m
  }, [filtered])

  const failedTotal = useMemo(() => filtered.filter((e) => e.status === 'failed').length, [filtered])

  // Snap to "today" on mount and again when data first arrives — but never on
  // later live updates, so SSE events don't yank the user's scroll position.
  const scrolledRef = useRef(false)
  useEffect(() => {
    if (scrolledRef.current) return
    const el = scrollRef.current
    if (el) el.scrollLeft = el.scrollWidth
    if (events.length) scrolledRef.current = true
  }, [events])

  const selectedAgg = selected ? byDay.get(selected) : undefined

  const onEnter = (el: HTMLElement, key: string, day: Date, agg?: DayAgg) => {
    const wrap = wrapRef.current
    if (!wrap) return
    const r = el.getBoundingClientRect()
    const wr = wrap.getBoundingClientRect()
    const label = `${day.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })} — ${summarize(agg)}`
    setHover({ key, label, x: r.left - wr.left + r.width / 2, y: r.top - wr.top })
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-2 space-y-0">
        <div>
          <CardTitle>Backup activity</CardTitle>
          <CardDescription>
            {loading
              ? 'Loading run history…'
              : `${filtered.length} ${kind === 'all' ? 'job' : kind.toLowerCase()} runs recorded in the last year${failedTotal ? ` · ${failedTotal} failed` : ' · no failures'}`}
          </CardDescription>
        </div>
        <div className="flex gap-1">
          {(['Backup', 'Check', 'Prune', 'Restore', 'all'] as const).map((k) => (
            <button
              key={k}
              onClick={() => {
                setKind(k)
                setSelected(null)
              }}
              className={cn(
                'rounded-md border px-2 py-1 text-xs',
                kind === k
                  ? 'border-primary bg-primary text-primary-foreground'
                  : 'border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground',
              )}
            >
              {k === 'all' ? 'All' : `${k}s`}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        <div ref={wrapRef} className="relative">
          {hover && (
            <div
              className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-md border bg-background px-2 py-1 text-xs shadow-md"
              style={{ left: hover.x, top: hover.y - 4 }}
            >
              {hover.label}
            </div>
          )}
          <div ref={scrollRef} className="overflow-x-auto pb-1">
            <div className="inline-flex flex-col">
              {/* month labels */}
              <div className="ml-8 flex" style={{ gap: GAP }}>
                {weeks.map((col, i) => {
                  const label =
                    i === 0 || col[0].getMonth() !== weeks[i - 1][0].getMonth()
                      ? MONTHS[col[0].getMonth()]
                      : ''
                  return (
                    <div key={i} className="relative" style={{ width: CELL, height: 16 }}>
                      {label && (
                        <span className="absolute left-0 top-0 whitespace-nowrap text-[10px] text-muted-foreground">
                          {label}
                        </span>
                      )}
                    </div>
                  )
                })}
              </div>
              <div className="flex">
                {/* weekday labels */}
                <div
                  className="mr-1 flex w-7 flex-col text-[10px] text-muted-foreground"
                  style={{ gap: GAP }}
                >
                  {['', 'Mon', '', 'Wed', '', 'Fri', ''].map((d, i) => (
                    <span key={i} className="text-right leading-none" style={{ height: CELL }}>
                      {d}
                    </span>
                  ))}
                </div>
                {/* cells */}
                <div className="flex" style={{ gap: GAP }}>
                  {weeks.map((col, wi) => (
                    <div key={wi} className="flex flex-col" style={{ gap: GAP }}>
                      {col.map((day) => {
                        const key = dayKey(day)
                        if (key > todayKey) {
                          return <div key={key} style={{ width: CELL, height: CELL }} />
                        }
                        const agg = byDay.get(key)
                        return (
                          <button
                            key={key}
                            aria-label={`${key}: ${summarize(agg)}`}
                            onMouseEnter={(e) => onEnter(e.currentTarget, key, day, agg)}
                            onMouseLeave={() => setHover(null)}
                            onClick={() => setSelected(selected === key ? null : key)}
                            className={cn(
                              'rounded-[2px]',
                              selected === key && 'ring-2 ring-ring ring-offset-1 ring-offset-background',
                            )}
                            style={{ width: CELL, height: CELL, background: cellColor(agg) }}
                          />
                        )
                      })}
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>
          {/* legend */}
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
            <span className="flex items-center gap-1">
              Less
              {['var(--activity-empty)', 'var(--activity-l1)', 'var(--activity-l2)', 'var(--activity-l3)', 'var(--activity-l4)'].map(
                (c) => (
                  <span key={c} className="inline-block rounded-[2px]" style={{ width: CELL, height: CELL, background: c }} />
                ),
              )}
              More
            </span>
            <span className="flex items-center gap-1">
              <span className="inline-block rounded-[2px]" style={{ width: CELL, height: CELL, background: 'var(--activity-mixed)' }} />
              some failed
            </span>
            <span className="flex items-center gap-1">
              <span className="inline-block rounded-[2px]" style={{ width: CELL, height: CELL, background: 'var(--activity-failed)' }} />
              all failed
            </span>
          </div>
        </div>

        {selected && (
          <div className="mt-4 border-t pt-3">
            <div className="mb-2 text-sm font-medium">
              {new Date(`${selected}T00:00:00`).toLocaleDateString(undefined, {
                weekday: 'long',
                year: 'numeric',
                month: 'long',
                day: 'numeric',
              })}{' '}
              <span className="font-normal text-muted-foreground">— {summarize(selectedAgg)}</span>
            </div>
            {selectedAgg ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Time</TableHead>
                    <TableHead>Kind</TableHead>
                    <TableHead>Job</TableHead>
                    <TableHead>Schedule</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Message</TableHead>
                    <TableHead className="text-right">Restore point</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {[...selectedAgg.events]
                    .sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime())
                    .map((e) => {
                      const snaps = matchSnapshots(e, snapshots)
                      return (
                        <TableRow key={e.uid}>
                          <TableCell className="whitespace-nowrap text-xs">
                            {new Date(e.startedAt).toLocaleTimeString()}
                          </TableCell>
                          <TableCell className="text-xs">{e.kind}</TableCell>
                          <TableCell className="font-mono text-xs">
                            <Link
                              to={`/jobs?namespace=${encodeURIComponent(e.namespace)}`}
                              className="hover:underline"
                              title="Open in Jobs"
                            >
                              {e.namespace}/{e.name}
                            </Link>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{e.schedule || '—'}</TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                e.status === 'succeeded'
                                  ? 'success'
                                  : e.status === 'failed'
                                    ? 'danger'
                                    : e.status === 'running'
                                      ? 'secondary'
                                      : 'warning'
                              }
                            >
                              {e.status}
                            </Badge>
                          </TableCell>
                          <TableCell className="max-w-[280px] truncate text-xs text-muted-foreground" title={e.message}>
                            {e.message || '—'}
                          </TableCell>
                          <TableCell className="text-right">
                            {snaps.length > 0 ? (
                              <div className="flex flex-col items-end gap-1">
                                {snaps.map((s) => (
                                  <div key={`${s.namespace}/${s.name}`} className="flex items-center gap-1">
                                    <span
                                      className="max-w-[140px] truncate font-mono text-[11px] text-muted-foreground"
                                      title={(snapSpec(s).paths || []).join(', ')}
                                    >
                                      {workloadFromPaths(snapSpec(s).paths || [])}
                                    </span>
                                    <Button asChild size="sm" variant="ghost">
                                      <Link to={`/snapshots/${s.namespace}/${s.name}/browse`}>
                                        <FolderSearch className="h-3.5 w-3.5" />
                                        Browse
                                      </Link>
                                    </Button>
                                    {isSqlDump(s) ? (
                                      <Button size="sm" variant="secondary" onClick={() => setRecoverSnap(s)}>
                                        Recover…
                                      </Button>
                                    ) : (
                                      <Button size="sm" variant="secondary" onClick={() => setRestoreSnap(s)}>
                                        Restore…
                                      </Button>
                                    )}
                                  </div>
                                ))}
                              </div>
                            ) : e.kind === 'Backup' && e.status === 'succeeded' ? (
                              <span className="text-xs text-muted-foreground">no snapshot found</span>
                            ) : (
                              <span className="text-xs text-muted-foreground">—</span>
                            )}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                </TableBody>
              </Table>
            ) : (
              <p className="text-sm text-muted-foreground">No runs recorded on this day.</p>
            )}
          </div>
        )}

        <RestoreSnapshotDialog snapshot={restoreSnap} onClose={() => setRestoreSnap(null)} />
        <RecoveryDialog snapshot={recoverSnap} onClose={() => setRecoverSnap(null)} />
      </CardContent>
    </Card>
  )
}
