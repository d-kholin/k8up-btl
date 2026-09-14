import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import PageHeader from '../components/PageHeader'
import { coverageResources } from '../lib/coverage'
import { Play } from 'lucide-react'
import { api, type BackupEvent, type K8sObject, type PVCRef, type StorageStats } from '../api'
import { flattenLiveJobs, type LiveJob } from '../lib/jobs'
import { snapTime } from '../lib/snapshots'
import { cronIntervalMs, formatAge, formatBytes, formatWhen } from '../lib/utils'
import ActiveJobs from '../components/ActiveJobs'
import JobConsole from '../components/JobConsole'
import RunBackupDialog, { type NamespaceOption } from '../components/RunBackupDialog'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Input } from '../components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../components/ui/table'

type Row = {
  namespace: string
  cron?: string
  hasSchedule: boolean
  last?: BackupEvent
  runningKinds: string[]
  snapshotCount: number
  latestSnap?: number
  storedBytes?: number
  state: 'fresh' | 'stale' | 'never' | 'no-schedule'
}

const DAY = 24 * 3_600_000

export default function Workloads() {
  const [schedules, setSchedules] = useState<K8sObject[]>([])
  const [snapshots, setSnapshots] = useState<K8sObject[]>([])
  const [pvcs, setPvcs] = useState<PVCRef[] | null>(null)
  const [history, setHistory] = useState<BackupEvent[]>([])
  const [storage, setStorage] = useState<StorageStats | null>(null)
  const [live, setLive] = useState<LiveJob[]>([])
  const [pending, setPending] = useState<LiveJob[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [params, setParams] = useSearchParams()
  const filter = params.get('q') || ''
  const setFilter = (value: string) => setParams(value ? { q: value } : {}, { replace: true })
  const coverage = useMemo(
    () => coverageResources(schedules, snapshots, pvcs),
    [schedules, snapshots, pvcs],
  )
  const [dialogNs, setDialogNs] = useState<string | undefined>()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [consoleJob, setConsoleJob] = useState<LiveJob | null>(null)
  const [consoleOpen, setConsoleOpen] = useState(false)

  const loadJobs = () =>
    api
      .jobs()
      .then((j) => setLive(flattenLiveJobs(j)))
      .catch(() => {
        /* keep last */
      })

  const refresh = (silent: boolean) => {
    Promise.all([api.schedules(), api.snapshots(), api.backupHistory(90)])
      .then(([sch, snaps, h]) => {
        setSchedules(sch)
        setSnapshots(snaps)
        setHistory(h.events)
        setError('')
      })
      .catch((e: Error) => {
        setError((silent ? 'Refresh failed; showing previous data. ' : '') + e.message)
      })
      .finally(() => setLoading(false))
    api
      .pvcs()
      .then(setPvcs)
      .catch(() => setPvcs(null))
    loadJobs()
  }

  useEffect(() => {
    refresh(false)
    api
      .storageStats()
      .then(setStorage)
      .catch(() => {})
    const t = setInterval(() => refresh(true), 60000)
    const tj = setInterval(loadJobs, 10000)
    return () => {
      clearInterval(t)
      clearInterval(tj)
    }
  }, [])

  // Poll while restic stats compute in the background (same pattern as Dashboard).
  useEffect(() => {
    if (!storage?.computing) return
    const t = setInterval(
      () =>
        api
          .storageStats()
          .then(setStorage)
          .catch(() => {}),
      3000,
    )
    return () => clearInterval(t)
  }, [storage?.computing])

  // Live outcome transitions from the history sweeper.
  useEffect(() => {
    const es = new EventSource('/api/v1/events')
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as { type?: string; data?: BackupEvent }
        if (msg.type === 'backup-event' && msg.data?.uid) {
          const e = msg.data
          setHistory((prev) => {
            const i = prev.findIndex((x) => x.uid === e.uid)
            if (i === -1) return [e, ...prev]
            const next = [...prev]
            next[i] = e
            return next
          })
          loadJobs()
        }
      } catch {
        /* ignore malformed events */
      }
    }
    return () => es.close()
  }, [])

  // Merge just-created jobs the 10s poll hasn't picked up yet.
  const activeJobs = useMemo(() => {
    const merged = [...live]
    for (const p of pending) {
      if (!live.some((l) => l.namespace === p.namespace && l.name === p.name)) merged.push(p)
    }
    return merged
  }, [live, pending])

  const rows = useMemo(() => {
    const now = Date.now()

    const cronByNs = new Map<string, string | undefined>()
    for (const s of schedules) {
      const ns = s.namespace || 'default'
      const cron = (s.spec as { backup?: { schedule?: string } } | undefined)?.backup?.schedule
      if (!cronByNs.has(ns) || (cron && !cronByNs.get(ns))) cronByNs.set(ns, cron)
    }

    const latestByNs = new Map<string, { when: number; count: number }>()
    for (const s of snapshots) {
      const ns = s.namespace || 'default'
      const when = snapTime(s)
      const cur = latestByNs.get(ns)
      if (!cur) latestByNs.set(ns, { when, count: 1 })
      else {
        cur.count++
        if (when > cur.when) cur.when = when
      }
    }

    const lastEventByNs = new Map<string, BackupEvent>()
    for (const e of history) {
      if (e.kind.toLowerCase() !== 'backup') continue
      const cur = lastEventByNs.get(e.namespace)
      if (!cur || new Date(e.startedAt) > new Date(cur.startedAt)) lastEventByNs.set(e.namespace, e)
    }

    const storedByNs = new Map<string, number>()
    for (const r of storage?.repos || []) storedByNs.set(r.namespace, r.storedBytes)

    const namespaces = new Set<string>([...cronByNs.keys(), ...latestByNs.keys()])
    for (const p of pvcs || []) namespaces.add(p.namespace)

    const out: Row[] = [...namespaces].map((ns) => {
      const hasSchedule = cronByNs.has(ns)
      const cron = cronByNs.get(ns)
      const latest = latestByNs.get(ns)
      const ageMs = latest ? now - latest.when : null

      let state: Row['state']
      if (!hasSchedule) state = 'no-schedule'
      else if (ageMs == null) state = 'never'
      else state = ageMs > (cronIntervalMs(cron) ?? DAY) * 1.5 ? 'stale' : 'fresh'

      return {
        namespace: ns,
        cron,
        hasSchedule,
        last: lastEventByNs.get(ns),
        runningKinds: activeJobs
          .filter((j) => j.namespace === ns && !j.finished)
          .map((j) => j.kind),
        snapshotCount: latest?.count ?? 0,
        latestSnap: latest?.when,
        storedBytes: storedByNs.get(ns),
        state,
      }
    })

    const weight = { never: 0, stale: 1, 'no-schedule': 2, fresh: 3 }
    out.sort((a, b) => weight[a.state] - weight[b.state] || a.namespace.localeCompare(b.namespace))
    return out
  }, [schedules, snapshots, history, storage, pvcs, activeJobs])

  const options: NamespaceOption[] = useMemo(
    () => rows.map((r) => ({ namespace: r.namespace, hasSchedule: r.hasSchedule })),
    [rows],
  )

  const q = filter.trim().toLowerCase()
  const visible = q ? rows.filter((r) => r.namespace.toLowerCase().includes(q)) : rows

  const openDialog = (ns?: string) => {
    setDialogNs(ns)
    setDialogOpen(true)
  }

  const onCreated = (kind: 'backup' | 'check', namespace: string, name: string) => {
    const job: LiveJob = {
      kind,
      namespace,
      name,
      createdAt: new Date().toISOString(),
      finished: false,
      failed: false,
    }
    setPending((prev) => [
      ...prev.filter((p) => Date.now() - new Date(p.createdAt || 0).getTime() < 5 * 60000),
      job,
    ])
    loadJobs()
    // Drop straight into the live console so the new job's progress is visible.
    setConsoleJob(job)
    setConsoleOpen(true)
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Workloads"
        description="Review backup coverage and run backups for each namespace."
        actions={
          <Button onClick={() => openDialog()}>
            <Play className="mr-1.5 h-4 w-4" /> Run backup…
          </Button>
        }
      />
      {error && <Alert variant="danger">{error}</Alert>}

      <ActiveJobs
        jobs={activeJobs}
        onSelect={(j) => {
          setConsoleJob(j)
          setConsoleOpen(true)
        }}
      />

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0">
          <div>
            <CardTitle>Namespaces</CardTitle>
            <CardDescription>
              Stored sizes come from cached restic stats
              {storage?.collectedAt ? ` (collected ${formatWhen(storage.collectedAt)})` : ''}
              {storage?.computing ? ' — refreshing…' : ''}
            </CardDescription>
          </div>
          <Input
            aria-label="Filter namespaces"
            placeholder="Filter namespaces…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="max-w-xs"
          />
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Namespace</TableHead>
                <TableHead className="hidden md:table-cell">Schedule</TableHead>
                <TableHead>Last backup</TableHead>
                <TableHead className="hidden text-right sm:table-cell">Coverage</TableHead>
                <TableHead className="hidden text-right lg:table-cell">Stored</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.map((r) => (
                <TableRow key={r.namespace}>
                  <TableCell className="font-mono text-xs">
                    <Link
                      to={`/workloads/${encodeURIComponent(r.namespace)}`}
                      className="hover:underline"
                    >
                      {r.namespace}
                    </Link>
                  </TableCell>
                  <TableCell className="hidden font-mono text-xs md:table-cell">
                    {r.cron || (r.hasSchedule ? '—' : <Badge variant="warning">no schedule</Badge>)}
                  </TableCell>
                  <TableCell>
                    <LastBackupCell row={r} />
                  </TableCell>
                  <TableCell className="hidden text-right text-xs sm:table-cell">
                    {(() => {
                      const resources = coverage.filter(
                        (c) => c.namespace === r.namespace && c.state !== 'excluded',
                      )
                      const current = resources.filter((c) => c.state === 'fresh').length
                      return (
                        <Badge
                          variant={
                            !error &&
                            pvcs !== null &&
                            resources.length > 0 &&
                            current === resources.length
                              ? 'success'
                              : 'warning'
                          }
                        >
                          {error
                            ? 'Unavailable'
                            : current +
                              ' / ' +
                              resources.length +
                              ' current' +
                              (pvcs === null ? ' · partial' : '')}
                        </Badge>
                      )
                    })()}
                  </TableCell>
                  <TableCell className="hidden text-right text-xs lg:table-cell">
                    {formatBytes(r.storedBytes)}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="outline" onClick={() => openDialog(r.namespace)}>
                      Backup
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {visible.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    {loading
                      ? 'Loading…'
                      : 'No namespaces with schedules, snapshots, or PVCs found.'}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <RunBackupDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        options={options}
        initialNamespace={dialogNs}
        onCreated={onCreated}
      />

      <JobConsole
        job={
          consoleJob &&
          (activeJobs.find(
            (j) =>
              j.kind === consoleJob.kind &&
              j.namespace === consoleJob.namespace &&
              j.name === consoleJob.name,
          ) ??
            consoleJob)
        }
        open={consoleOpen}
        onOpenChange={setConsoleOpen}
      />
    </div>
  )
}

function LastBackupCell({ row }: { row: Row }) {
  const now = Date.now()
  if (row.runningKinds.includes('backup')) return <Badge variant="secondary">running…</Badge>
  const e = row.last
  if (e) {
    const when = new Date(e.finishedAt || e.startedAt).getTime()
    const age = formatAge(now - when)
    if (e.status === 'failed')
      return (
        <span className="flex items-center gap-1.5" title={e.message || undefined}>
          <Badge variant="danger">failed</Badge>
          <span className="text-xs text-muted-foreground">{age} ago</span>
        </span>
      )
    if (e.status === 'running') return <Badge variant="secondary">running…</Badge>
    if (e.status === 'succeeded')
      return (
        <span className="flex items-center gap-1.5">
          <Badge variant="success">Succeeded</Badge>
          <span className="text-xs text-muted-foreground">{age} ago</span>
          {row.state === 'stale' && <Badge variant="warning">stale</Badge>}
        </span>
      )
  }
  // No recorded run (fresh install / history pruned): fall back to snapshots.
  if (row.latestSnap)
    return (
      <span className="flex items-center gap-1.5">
        <Badge variant="secondary">Snapshot found</Badge>
        <span className="text-xs text-muted-foreground">{formatAge(now - row.latestSnap)} ago</span>
        {row.state === 'stale' && <Badge variant="warning">stale</Badge>}
      </span>
    )
  return <Badge variant={row.hasSchedule ? 'danger' : 'secondary'}>never</Badge>
}
