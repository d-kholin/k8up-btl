import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, ChevronDown, FolderSearch, Play } from 'lucide-react'
import { api, type BackupEvent, type K8sObject, type StorageStats } from '../api'
import { flattenLiveJobs, type LiveJob } from '../lib/jobs'
import { isSqlDump, snapSpec, snapTime, sourcePvcCandidates, workloadFromPaths } from '../lib/snapshots'
import { formatAge, formatBytes, formatWhen } from '../lib/utils'
import ActiveJobs from '../components/ActiveJobs'
import RunBackupDialog from '../components/RunBackupDialog'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

type Condition = { type?: string; status?: string }

function conditionReady(s: K8sObject): boolean {
  const conds = ((s.status as { conditions?: Condition[] } | undefined)?.conditions || []) as Condition[]
  return conds.some((c) => c.type === 'Ready' && c.status === 'True')
}

type ScheduleSpec = {
  backup?: { schedule?: string }
  check?: { schedule?: string }
  prune?: { schedule?: string; retention?: Record<string, unknown> }
  archive?: { schedule?: string }
  backend?: { s3?: { bucket?: string } }
}

export default function WorkloadDetail() {
  const { ns = '' } = useParams()
  const [schedules, setSchedules] = useState<K8sObject[]>([])
  const [snapshots, setSnapshots] = useState<K8sObject[]>([])
  const [history, setHistory] = useState<BackupEvent[]>([])
  const [storage, setStorage] = useState<StorageStats | null>(null)
  const [live, setLive] = useState<LiveJob[]>([])
  const [pending, setPending] = useState<LiveJob[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [dialogOpen, setDialogOpen] = useState(false)

  const loadJobs = () =>
    api
      .jobs()
      .then((j) => setLive(flattenLiveJobs(j).filter((x) => x.namespace === ns)))
      .catch(() => {})

  const refresh = (silent: boolean) => {
    Promise.all([api.schedules(), api.snapshots(ns), api.backupHistory(90)])
      .then(([sch, snaps, h]) => {
        setSchedules(sch.filter((s) => s.namespace === ns))
        setSnapshots(snaps)
        setHistory(h.events.filter((e) => e.namespace === ns))
        setError('')
      })
      .catch((e: Error) => {
        if (!silent) setError(e.message)
      })
      .finally(() => setLoading(false))
    loadJobs()
  }

  useEffect(() => {
    setLoading(true)
    refresh(false)
    api.storageStats().then(setStorage).catch(() => {})
    const t = setInterval(() => refresh(true), 60000)
    const tj = setInterval(loadJobs, 10000)
    return () => {
      clearInterval(t)
      clearInterval(tj)
    }
  }, [ns])

  useEffect(() => {
    if (!storage?.computing) return
    const t = setInterval(() => api.storageStats().then(setStorage).catch(() => {}), 3000)
    return () => clearInterval(t)
  }, [storage?.computing])

  useEffect(() => {
    const es = new EventSource('/api/v1/events')
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as { type?: string; data?: BackupEvent }
        if (msg.type === 'backup-event' && msg.data?.uid && msg.data.namespace === ns) {
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
  }, [ns])

  const activeJobs = useMemo(() => {
    const merged = [...live]
    for (const p of pending) {
      if (!live.some((l) => l.namespace === p.namespace && l.name === p.name)) merged.push(p)
    }
    return merged
  }, [live, pending])

  const repo = storage?.repos.find((r) => r.namespace === ns)

  // Group snapshots by source PVC (SQL dumps group under their dump file label).
  const byPvc = useMemo(() => {
    const groups = new Map<string, { kind: 'pvc' | 'sql'; count: number; latest: K8sObject }>()
    for (const s of snapshots) {
      const pvcs = sourcePvcCandidates(s)
      const sql = isSqlDump(s)
      const keys = pvcs.length ? pvcs : [workloadFromPaths(snapSpec(s).paths || [])]
      for (const key of keys) {
        const cur = groups.get(key)
        if (!cur) groups.set(key, { kind: sql && !pvcs.length ? 'sql' : 'pvc', count: 1, latest: s })
        else {
          cur.count++
          if (snapTime(s) > snapTime(cur.latest)) cur.latest = s
        }
      }
    }
    return [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [snapshots])

  const events = useMemo(
    () =>
      [...history]
        .sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime())
        .slice(0, 25),
    [history],
  )

  const now = Date.now()

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <Link
            to="/workloads"
            className="mb-1 flex items-center gap-1 text-xs text-muted-foreground hover:underline"
          >
            <ArrowLeft className="h-3 w-3" /> Workloads
          </Link>
          <h1 className="font-mono text-2xl font-semibold tracking-tight">{ns}</h1>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button asChild variant="outline">
            <Link to={`/snapshots?namespace=${encodeURIComponent(ns)}`}>
              <FolderSearch className="mr-1.5 h-4 w-4" /> Snapshots & restore
            </Link>
          </Button>
          <Button onClick={() => setDialogOpen(true)}>
            <Play className="mr-1.5 h-4 w-4" /> Run backup…
          </Button>
        </div>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}

      <ActiveJobs jobs={activeJobs} linkNamespace={false} />

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Schedule</CardTitle>
            <CardDescription>K8up Schedule CRs in this namespace</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            {schedules.length === 0 && (
              <p className="text-muted-foreground">
                {loading ? 'Loading…' : 'No Schedule — this namespace is not backed up on a cadence.'}
              </p>
            )}
            {schedules.map((s) => {
              const spec = (s.spec || {}) as ScheduleSpec
              const retention = spec.prune?.retention
              return (
                <div key={s.name} className="space-y-1.5">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs font-medium">{s.name}</span>
                    <Badge variant={conditionReady(s) ? 'success' : 'secondary'}>
                      {conditionReady(s) ? 'ready' : 'unknown'}
                    </Badge>
                  </div>
                  <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
                    {(['backup', 'check', 'prune', 'archive'] as const).map((k) =>
                      spec[k]?.schedule ? (
                        <FieldRow key={k} label={k} value={spec[k]?.schedule || ''} mono />
                      ) : null,
                    )}
                    {spec.backend?.s3?.bucket && (
                      <FieldRow label="bucket" value={spec.backend.s3.bucket} mono />
                    )}
                    {retention && Object.keys(retention).length > 0 && (
                      <FieldRow
                        label="retention"
                        value={Object.entries(retention)
                          .map(([k, v]) => `${k.replace(/^keep/, '').toLowerCase()}: ${String(v)}`)
                          .join(', ')}
                      />
                    )}
                  </dl>
                </div>
              )
            })}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Repository storage</CardTitle>
            <CardDescription>
              From cached restic stats
              {storage?.collectedAt ? ` — collected ${formatWhen(storage.collectedAt)}` : ''}
              {storage?.computing ? ' (refreshing…)' : ''}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {!repo && (
              <p className="text-muted-foreground">
                {storage?.computing ? 'Computing…' : 'No repository stats for this namespace yet.'}
              </p>
            )}
            {repo?.error && <Alert variant="warning">{repo.error}</Alert>}
            {repo && (
              <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5">
                <FieldRow label="Stored (post-dedup)" value={formatBytes(repo.storedBytes)} />
                <FieldRow label="Logical data" value={formatBytes(repo.logicalBytes)} />
                <FieldRow label="Dedup savings" value={`${Math.round(repo.dedupRatio * 100)}%`} />
                <FieldRow label="Snapshots" value={String(repo.snapshotCount)} />
                <FieldRow label="Repository" value={repo.repository} mono />
              </dl>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Restore points</CardTitle>
          <CardDescription>Snapshots grouped by source PVC — restore from the snapshot list</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Source</TableHead>
                <TableHead className="text-right">Snapshots</TableHead>
                <TableHead className="hidden sm:table-cell">Latest</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {byPvc.map(([key, g]) => (
                <TableRow key={key}>
                  <TableCell className="font-mono text-xs">
                    {key}
                    {g.kind === 'sql' && (
                      <Badge variant="secondary" className="ml-2">
                        sql dump
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right text-xs">{g.count}</TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground sm:table-cell">
                    {formatWhen(snapSpec(g.latest).date || g.latest.creationTimestamp)}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button asChild variant="link" size="sm">
                        <Link to={`/snapshots/${g.latest.namespace}/${g.latest.name}/browse`}>Browse latest</Link>
                      </Button>
                      <Button asChild variant="link" size="sm">
                        <Link to={`/snapshots?namespace=${encodeURIComponent(ns)}`}>All</Link>
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {byPvc.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    {loading ? 'Loading…' : 'No snapshots in this namespace yet.'}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Job history</CardTitle>
          <CardDescription>Recent Backup / Check / Prune runs (last 90 days)</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead className="hidden md:table-cell">Job</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Message</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.map((e) => (
                <EventRows key={e.uid} event={e} now={now} />
              ))}
              {events.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground">
                    {loading ? 'Loading…' : 'No recorded runs for this namespace.'}
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
        options={[{ namespace: ns, hasSchedule: schedules.length > 0 }]}
        initialNamespace={ns}
        onCreated={(kind, namespace, name) => {
          setPending((prev) => [
            ...prev.filter((p) => Date.now() - new Date(p.createdAt || 0).getTime() < 5 * 60000),
            { kind, namespace, name, createdAt: new Date().toISOString(), finished: false, failed: false },
          ])
          loadJobs()
        }}
      />
    </div>
  )
}

/** One history entry; failed runs with captured pod detail expand to show it. */
function EventRows({ event: e, now }: { event: BackupEvent; now: number }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <TableRow>
        <TableCell className="whitespace-nowrap text-xs text-muted-foreground" title={formatWhen(e.startedAt)}>
          {formatAge(now - new Date(e.startedAt).getTime())} ago
        </TableCell>
        <TableCell className="text-xs capitalize">{e.kind}</TableCell>
        <TableCell className="hidden font-mono text-xs md:table-cell">{e.name}</TableCell>
        <TableCell>
          <Badge
            variant={
              e.status === 'succeeded' ? 'success' : e.status === 'failed' ? 'danger' : 'secondary'
            }
          >
            {e.status}
          </Badge>
        </TableCell>
        <TableCell className="max-w-md whitespace-pre-wrap break-words text-xs text-muted-foreground">
          {e.message || '—'}
          {e.detail && (
            <button
              type="button"
              onClick={() => setOpen((o) => !o)}
              className="ml-2 inline-flex items-center gap-0.5 text-primary hover:underline"
            >
              {open ? 'hide detail' : 'show detail'}
              <ChevronDown className={`h-3 w-3 transition-transform ${open ? 'rotate-180' : ''}`} />
            </button>
          )}
        </TableCell>
      </TableRow>
      {e.detail && open && (
        <TableRow>
          <TableCell colSpan={5} className="bg-muted/40">
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md p-2 font-mono text-[11px] leading-relaxed text-muted-foreground">
              {e.detail}
            </pre>
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

function FieldRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? 'break-all font-mono' : ''}>{value}</dd>
    </>
  )
}
