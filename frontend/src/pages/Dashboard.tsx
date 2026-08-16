import { useEffect, useMemo, useState, type ComponentType } from 'react'
import { Link } from 'react-router-dom'
import { Archive, CheckCircle2, Clock3, Database, HardDrive, History, AlertTriangle } from 'lucide-react'
import { api, type AuditEntry, type K8sObject, type RestoreState } from '../api'
import { formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

type SnapSpec = { date?: string; id?: string; paths?: string[]; repository?: string }

function snapSpec(s: K8sObject): SnapSpec {
  return (s.spec || {}) as SnapSpec
}

function jobList(v: K8sObject[] | { error: string } | undefined): K8sObject[] {
  return Array.isArray(v) ? v : []
}

function conditionReady(s: K8sObject): boolean {
  const st = s.status as { conditions?: Array<{ type?: string; status?: string }> } | undefined
  return !!st?.conditions?.some((c) => c.type === 'Ready' && c.status === 'True')
}

export default function Dashboard() {
  const [schedules, setSchedules] = useState<K8sObject[]>([])
  const [snapshots, setSnapshots] = useState<K8sObject[]>([])
  const [jobs, setJobs] = useState<Record<string, K8sObject[] | { error: string }>>({})
  const [audit, setAudit] = useState<AuditEntry[]>([])
  const [interrupted, setInterrupted] = useState<RestoreState[]>([])
  const [meta, setMeta] = useState<{ grafanaDashboardUrl?: string } | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = () => {
    setLoading(true)
    Promise.all([
      api.schedules(),
      api.snapshots(),
      api.jobs(),
      api.audit(),
      api.interrupted(),
      api.meta(),
    ])
      .then(([sch, sn, j, a, i, m]) => {
        setSchedules(sch)
        setSnapshots(sn)
        setJobs(j)
        setAudit(a)
        setInterrupted(i)
        setMeta(m)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
  }, [])

  const stats = useMemo(() => {
    const ns = new Set(snapshots.map((s) => s.namespace).filter(Boolean))
    const restorePts = snapshots.length
    const namespaces = ns.size
    const schedulesReady = schedules.filter(conditionReady).length

    const backups = jobList(jobs.backups)
    const restores = jobList(jobs.restores)
    const checks = jobList(jobs.checks)
    const prunes = jobList(jobs.prunes)

    const recentSnap = [...snapshots].sort((a, b) => {
      const ta = new Date(snapSpec(a).date || a.creationTimestamp || 0).getTime()
      const tb = new Date(snapSpec(b).date || b.creationTimestamp || 0).getTime()
      return tb - ta
    })[0]

    const restoreAudit = audit.filter((e) => e.kind === 'restore')
    const restoreOk = restoreAudit.filter((e) => e.status === 'success').length
    const restoreFail = restoreAudit.filter((e) => e.status === 'failed').length
    const bytesDownloaded = audit
      .filter((e) => e.kind === 'download')
      .reduce((n, e) => n + (e.bytes || 0), 0)

    // Workload keys from snapshot paths (approx restore targets)
    const workloads = new Set(
      snapshots.flatMap((s) =>
        (snapSpec(s).paths || []).map((p) => {
          const base = p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || p
          return `${s.namespace}/${base}`
        }),
      ),
    )

    return {
      restorePts,
      namespaces,
      workloads: workloads.size,
      schedules: schedules.length,
      schedulesReady,
      backups: backups.length,
      restoresOpen: restores.length,
      checks: checks.length,
      prunes: prunes.length,
      recentSnap,
      restoreOk,
      restoreFail,
      bytesDownloaded,
      latestRestore: restoreAudit[0],
    }
  }, [schedules, snapshots, jobs, audit])

  const byNamespace = useMemo(() => {
    const m = new Map<string, number>()
    for (const s of snapshots) {
      const ns = s.namespace || 'default'
      m.set(ns, (m.get(ns) || 0) + 1)
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [snapshots])

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-sm text-muted-foreground">
          Cluster-wide backup posture — restore points, jobs, and recent activity.
        </p>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}

      {interrupted.length > 0 && (
        <Alert variant="warning">
          <div className="flex items-start gap-2">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <div className="space-y-2">
              <div className="font-medium">Interrupted restore — Argo controller may still be stopped</div>
              <ul className="space-y-1 text-sm">
                {interrupted.map((r) => (
                  <li key={r.restoreId} className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">
                      {r.application
                        ? `${r.application.namespace}/${r.application.name}`
                        : 'global pause'}
                    </span>
                    <Badge variant="warning">{r.step}</Badge>
                    {r.lastError && <span className="text-xs opacity-90">{r.lastError}</span>}
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() =>
                        api
                          .resumeArgo('argocd', 'application-controller')
                          .then(load)
                          .catch((e: Error) => setError(e.message))
                      }
                    >
                      Resume Argo controller
                    </Button>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </Alert>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="Restore points"
          value={loading ? '…' : String(stats.restorePts)}
          hint={`${stats.namespaces} namespaces · ${stats.workloads} workloads`}
          icon={HardDrive}
        />
        <StatCard
          title="Schedules"
          value={loading ? '…' : String(stats.schedules)}
          hint={`${stats.schedulesReady} ready`}
          icon={Clock3}
        />
        <StatCard
          title="GUI restores"
          value={loading ? '…' : `${stats.restoreOk} ok`}
          hint={stats.restoreFail ? `${stats.restoreFail} failed (audit)` : 'no failures in audit'}
          icon={History}
        />
        <StatCard
          title="Downloads"
          value={loading ? '…' : formatBytes(stats.bytesDownloaded)}
          hint="file browser dumps (audit)"
          icon={Archive}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Restore points by namespace</CardTitle>
            <CardDescription>Snapshot CRs currently known to the cluster</CardDescription>
          </CardHeader>
          <CardContent>
            {byNamespace.length === 0 ? (
              <p className="text-sm text-muted-foreground">No snapshots yet.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Namespace</TableHead>
                    <TableHead className="text-right">Snapshots</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {byNamespace.map(([ns, n]) => (
                    <TableRow key={ns}>
                      <TableCell className="font-mono text-xs">{ns}</TableCell>
                      <TableCell className="text-right">{n}</TableCell>
                      <TableCell className="text-right">
                        <Button asChild variant="link" size="sm">
                          <Link to="/snapshots">Open</Link>
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Live K8up jobs</CardTitle>
            <CardDescription>CR counts cluster-wide</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Row label="Backups" value={stats.backups} />
            <Row label="Restores" value={stats.restoresOpen} />
            <Row label="Checks" value={stats.checks} />
            <Row label="Prunes" value={stats.prunes} />
            <div className="pt-2">
              <Button asChild variant="outline" size="sm" className="w-full">
                <Link to="/jobs">Manage jobs</Link>
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Database className="h-4 w-4" /> Latest snapshot
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {stats.recentSnap ? (
              <>
                <div className="font-mono text-xs">
                  {stats.recentSnap.namespace}/{stats.recentSnap.name}
                </div>
                <div className="text-muted-foreground">
                  {formatWhen(snapSpec(stats.recentSnap).date || stats.recentSnap.creationTimestamp)}
                </div>
                <div className="text-muted-foreground">
                  {(snapSpec(stats.recentSnap).paths || []).join(', ') || '—'}
                </div>
                <Button asChild size="sm" variant="secondary">
                  <Link
                    to={`/snapshots/${stats.recentSnap.namespace}/${stats.recentSnap.name}/browse`}
                  >
                    Browse files
                  </Link>
                </Button>
              </>
            ) : (
              <p className="text-muted-foreground">No snapshots.</p>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4" /> Latest GUI restore
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {stats.latestRestore ? (
              <>
                <div className="flex items-center gap-2">
                  <Badge
                    variant={
                      stats.latestRestore.status === 'success'
                        ? 'success'
                        : stats.latestRestore.status === 'failed'
                          ? 'danger'
                          : 'secondary'
                    }
                  >
                    {stats.latestRestore.status || '—'}
                  </Badge>
                  <span className="text-muted-foreground">{formatWhen(stats.latestRestore.at)}</span>
                </div>
                <div className="font-mono text-xs">
                  {stats.latestRestore.namespace}/{stats.latestRestore.pvc}
                </div>
                <div className="text-muted-foreground truncate" title={stats.latestRestore.snapshot}>
                  snap {stats.latestRestore.snapshot?.slice(0, 16)}…
                </div>
                <Button asChild size="sm" variant="secondary">
                  <Link to="/restores">Restore history</Link>
                </Button>
              </>
            ) : (
              <p className="text-muted-foreground">No restores logged yet.</p>
            )}
            {meta?.grafanaDashboardUrl && (
              <a
                className="block text-sm text-primary hover:underline"
                href={meta.grafanaDashboardUrl}
                target="_blank"
                rel="noreferrer"
              >
                K8up Grafana dashboard ↗
              </a>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <div>
            <CardTitle>Schedules</CardTitle>
            <CardDescription>Configured backup schedules</CardDescription>
          </div>
          <Button asChild variant="outline" size="sm">
            <Link to="/snapshots">Snapshots</Link>
          </Button>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Namespace</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Backup cron</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {schedules.map((s) => (
                <TableRow key={`${s.namespace}/${s.name}`}>
                  <TableCell className="font-mono text-xs">{s.namespace}</TableCell>
                  <TableCell className="font-mono text-xs">{s.name}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {String(
                      (s.spec as { backup?: { schedule?: string } })?.backup?.schedule ?? '—',
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={conditionReady(s) ? 'success' : 'secondary'}>
                      {conditionReady(s) ? 'ready' : 'unknown'}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
              {schedules.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    No schedules.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}

function StatCard({
  title,
  value,
  hint,
  icon: Icon,
}: {
  title: string
  value: string
  hint: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">{title}</CardTitle>
        <Icon className="h-4 w-4 text-muted-foreground" />
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold tracking-tight">{value}</div>
        <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
  )
}

function Row({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center justify-between border-b border-border/50 py-1.5 last:border-0">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium">{value}</span>
    </div>
  )
}
