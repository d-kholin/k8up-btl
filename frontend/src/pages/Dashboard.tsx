import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRight, FlaskConical, RefreshCw } from 'lucide-react'
import {
  api,
  type BackupEvent,
  type K8sObject,
  type Meta,
  type PVCRef,
  type RestoreState,
  type StorageStats,
} from '../api'
import { coverageResources } from '../lib/coverage'
import { formatBytes, formatWhen } from '../lib/utils'
import BackupActivity from '../components/BackupActivity'
import BackupFreshness from '../components/BackupFreshness'
import RestoreVerification from '../components/RestoreVerification'
import PageHeader from '../components/PageHeader'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'

type Overview = {
  schedules: K8sObject[]
  snapshots: K8sObject[]
  pvcs: PVCRef[] | null
  restores: RestoreState[]
  history: BackupEvent[]
  meta: Meta | null
}
const initial: Overview = {
  schedules: [],
  snapshots: [],
  pvcs: null,
  restores: [],
  history: [],
  meta: null,
}

export default function Dashboard() {
  const [data, setData] = useState<Overview>(initial)
  const [errors, setErrors] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [updated, setUpdated] = useState('')
  const [storage, setStorage] = useState<StorageStats | null>(null)
  const [storageError, setStorageError] = useState('')
  const [storageBusy, setStorageBusy] = useState(false)
  const load = async () => {
    setRefreshing(true)
    const results = await Promise.allSettled([
      api.schedules(),
      api.snapshots(),
      api.pvcs(),
      api.restores(),
      api.backupHistory(366),
      api.meta(),
    ] as const)
    const names = [
      'Schedules',
      'Snapshots',
      'Volume inventory',
      'Restore operations',
      'Backup activity',
      'Cluster details',
    ]
    setErrors(
      results.flatMap((r, i) =>
        r.status === 'rejected'
          ? [names[i] + ' unavailable: ' + String(r.reason?.message || r.reason)]
          : [],
      ),
    )
    setData((prev) => ({
      schedules: results[0].status === 'fulfilled' ? results[0].value : prev.schedules,
      snapshots: results[1].status === 'fulfilled' ? results[1].value : prev.snapshots,
      pvcs: results[2].status === 'fulfilled' ? results[2].value : null,
      restores: results[3].status === 'fulfilled' ? results[3].value : prev.restores,
      history: results[4].status === 'fulfilled' ? results[4].value.events : prev.history,
      meta: results[5].status === 'fulfilled' ? results[5].value : prev.meta,
    }))
    if (results.every((r) => r.status === 'fulfilled')) setUpdated(new Date().toISOString())
    setLoading(false)
    setRefreshing(false)
  }
  const loadStorage = async (refresh = false) => {
    setStorageBusy(true)
    try {
      setStorage(await api.storageStats(refresh))
      setStorageError('')
    } catch (e) {
      setStorageError((e as Error).message)
    } finally {
      setStorageBusy(false)
    }
  }
  useEffect(() => {
    load()
    loadStorage()
    const timer = setInterval(load, 60000)
    return () => clearInterval(timer)
  }, [])
  useEffect(() => {
    if (!storage?.computing) return
    const timer = setInterval(() => loadStorage(), 5000)
    return () => clearInterval(timer)
  }, [storage?.computing])
  const labNamespace = data.meta?.restoreLab?.namespace
  const schedules = data.schedules.filter((s) => s.namespace !== labNamespace)
  const snapshots = data.snapshots.filter((s) => s.namespace !== labNamespace)
  const pvcs = data.pvcs?.filter((p) => p.namespace !== labNamespace) ?? null
  const resources = coverageResources(schedules, snapshots, pvcs)
  const issues = resources.filter((r) => !['fresh', 'excluded'].includes(r.state)).length
  const active = data.restores.filter((r) => !['done', 'failed'].includes(r.step))
  const stopped = data.restores.filter(
    (r) => r.argoSyncResumed === false && ['done', 'failed'].includes(r.step),
  )
  const incomplete = errors.length > 0
  return (
    <div className="space-y-6">
      <PageHeader
        title="Recovery readiness"
        description="Know what is backed up, what needs attention, and whether your restores have been tested."
        actions={
          <>
            <Button variant="outline" onClick={load} disabled={refreshing}>
              <RefreshCw className={'h-4 w-4 ' + (refreshing ? 'animate-spin' : '')} />
              Refresh
            </Button>
            <Button asChild>
              <Link to="/lab">
                <FlaskConical className="h-4 w-4" />
                Test a restore
              </Link>
            </Button>
          </>
        }
      />
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className={'h-2 w-2 rounded-full ' + (incomplete ? 'bg-amber-500' : 'bg-primary')} />
        {loading
          ? 'Checking recovery readiness…'
          : incomplete
            ? 'Some data is unavailable or stale'
            : 'Inventory updated'}
        {updated && <span>· Last complete update {formatWhen(updated)}</span>}
      </div>
      {errors.length > 0 && (
        <Alert variant="warning">
          <div className="space-y-1">
            {errors.map((error) => (
              <p key={error}>{error}</p>
            ))}
            <p>Previous results are retained where available. Refresh to retry.</p>
          </div>
        </Alert>
      )}
      {(active.length > 0 || stopped.length > 0) && (
        <Alert variant={stopped.length ? 'danger' : 'warning'}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="font-semibold">
                {stopped.length
                  ? 'A restore reports Argo CD was not resumed'
                  : 'Production restore in progress'}
              </p>
              <p className="mt-1">
                {stopped.length
                  ? 'Review the operation and recovery controls.'
                  : 'Argo CD reconciliation is paused globally during the restore.'}
              </p>
            </div>
            <Button asChild variant="outline">
              <Link to="/restores">
                View operation
                <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
          </div>
        </Alert>
      )}
      <div className="grid gap-4 md:grid-cols-3">
        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle>Needs attention</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex items-baseline gap-3">
              <span className="text-4xl font-semibold tracking-tight">
                {loading || incomplete ? '—' : issues}
              </span>
              <span className="text-sm text-muted-foreground">
                resources with missing, overdue, or uncertain backups
              </span>
            </div>
            <p className="mt-3 text-sm text-muted-foreground">
              {incomplete
                ? 'Resolve the unavailable data before assessing coverage.'
                : issues
                  ? 'Review the resources below before relying on a recovery point.'
                  : 'Check restore verification below to see which backups have been tested.'}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Restore points</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-4xl font-semibold tracking-tight">
              {loading || incomplete ? '—' : snapshots.length}
            </div>
            <Button asChild variant="link" className="mt-2 px-0">
              <Link to="/snapshots">
                Find a recovery point
                <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>
      {!errors.some((e) => e.startsWith('Schedules') || e.startsWith('Snapshots')) && (
        <BackupFreshness
          schedules={schedules}
          snapshots={snapshots}
          pvcs={pvcs}
          loading={loading}
        />
      )}
      {!loading && !errors.some((e) => e.startsWith('Schedules')) && (
        <RestoreVerification schedules={schedules} />
      )}
      <details className="rounded-lg border bg-card p-5">
        <summary className="cursor-pointer text-base font-semibold">
          Activity & storage{' '}
          <span className="ml-2 text-sm font-normal text-muted-foreground">
            Historical trends and repository usage
          </span>
        </summary>
        <div className="mt-5 space-y-5">
          <BackupActivity events={data.history} snapshots={snapshots} loading={loading} />
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-base font-semibold">Storage & deduplication</h2>
            <Button
              size="sm"
              variant="outline"
              disabled={storageBusy || storage?.computing}
              onClick={() => loadStorage(true)}
            >
              {storageBusy || storage?.computing ? 'Calculating…' : 'Refresh storage'}
            </Button>
          </div>
          {(storageError || storage?.error) && (
            <Alert variant="warning">{storageError || storage?.error}</Alert>
          )}
          {storage ? (
            <>
              <div className="grid gap-4 sm:grid-cols-3">
                {[
                  ['Logical data', formatBytes(storage.logicalBytes)],
                  ['Stored in repository', formatBytes(storage.storedBytes)],
                  ['Deduplication savings', formatBytes(storage.savedBytes)],
                ].map(([label, value]) => (
                  <div key={label}>
                    <p className="text-sm text-muted-foreground">{label}</p>
                    <p className="mt-1 text-2xl font-semibold">{value}</p>
                  </div>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">
                Collected {formatWhen(storage.collectedAt)} {storage.partial && '· Partial results'}{' '}
                {storage.stale && '· Stale'} {storage.computing && '· Computing'}
              </p>
              <div className="divide-y">
                {storage.repos.map((repo) => (
                  <div
                    key={repo.namespace}
                    className="flex flex-wrap justify-between gap-2 py-3 text-sm"
                  >
                    <span>{repo.namespace}</span>
                    <span>
                      {formatBytes(repo.storedBytes)} stored · {repo.snapshotCount} snapshots{' '}
                      {repo.error && <Badge variant="warning">Unavailable</Badge>}
                    </span>
                  </div>
                ))}
              </div>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              {storageBusy ? 'Calculating repository storage…' : 'Storage data unavailable.'}
            </p>
          )}
          {data.meta?.grafanaDashboardUrl && (
            <Button asChild variant="link">
              <a href={data.meta.grafanaDashboardUrl} target="_blank" rel="noreferrer">
                Open Grafana dashboard ↗
              </a>
            </Button>
          )}
        </div>
      </details>
    </div>
  )
}
