import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle } from 'lucide-react'
import type { K8sObject, PVCRef } from '../api'
import { cronIntervalMs, formatAge, formatWhen } from '../lib/utils'
import { Alert } from './ui/alert'
import { Badge } from './ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

type FreshnessRow = {
  namespace: string
  cron?: string
  intervalMs: number | null
  lastBackup?: string
  ageMs: number | null
  snapshotCount: number
  state: 'fresh' | 'stale' | 'never' | 'no-schedule'
}

const DAY = 24 * 3_600_000

function snapDate(s: K8sObject): string | undefined {
  const spec = s.spec as { date?: string } | undefined
  return spec?.date || s.creationTimestamp
}

export default function BackupFreshness({
  schedules,
  snapshots,
  pvcs,
  loading,
}: {
  schedules: K8sObject[]
  snapshots: K8sObject[]
  pvcs: PVCRef[] | null
  loading: boolean
}) {
  const { rows, uncovered, staleCount } = useMemo(() => {
    const now = Date.now()

    const cronByNs = new Map<string, string | undefined>()
    for (const s of schedules) {
      const ns = s.namespace || 'default'
      const cron = (s.spec as { backup?: { schedule?: string } } | undefined)?.backup?.schedule
      // A namespace can have several Schedules; keep the first with a backup cron.
      if (!cronByNs.has(ns) || (cron && !cronByNs.get(ns))) cronByNs.set(ns, cron)
    }

    const latestByNs = new Map<string, { when: string; count: number }>()
    for (const s of snapshots) {
      const ns = s.namespace || 'default'
      const when = snapDate(s)
      const cur = latestByNs.get(ns)
      if (!cur) {
        latestByNs.set(ns, { when: when || '', count: 1 })
      } else {
        cur.count++
        if (when && (!cur.when || new Date(when) > new Date(cur.when))) cur.when = when
      }
    }

    const namespaces = new Set<string>([...cronByNs.keys(), ...latestByNs.keys()])
    const rows: FreshnessRow[] = [...namespaces].map((ns) => {
      const cron = cronByNs.get(ns)
      const hasSchedule = cronByNs.has(ns)
      const latest = latestByNs.get(ns)
      const intervalMs = cronIntervalMs(cron)
      const ageMs = latest?.when ? now - new Date(latest.when).getTime() : null

      let state: FreshnessRow['state']
      if (!hasSchedule) state = 'no-schedule'
      else if (ageMs == null) state = 'never'
      // 1.5× the expected cadence as grace before a backup counts as stale
      // (unparseable cron → assume daily).
      else state = ageMs > (intervalMs ?? DAY) * 1.5 ? 'stale' : 'fresh'

      return {
        namespace: ns,
        cron,
        intervalMs,
        lastBackup: latest?.when || undefined,
        ageMs,
        snapshotCount: latest?.count ?? 0,
        state,
      }
    })

    const weight = { never: 0, stale: 1, 'no-schedule': 2, fresh: 3 }
    rows.sort((a, b) => weight[a.state] - weight[b.state] || a.namespace.localeCompare(b.namespace))

    const covered = new Set(namespaces)
    const uncoveredSet = new Map<string, number>()
    for (const p of pvcs || []) {
      if (!covered.has(p.namespace)) uncoveredSet.set(p.namespace, (uncoveredSet.get(p.namespace) || 0) + 1)
    }
    const uncovered = [...uncoveredSet.entries()].sort((a, b) => a[0].localeCompare(b[0]))
    const staleCount = rows.filter((r) => r.state === 'stale' || r.state === 'never').length
    return { rows, uncovered, staleCount }
  }, [schedules, snapshots, pvcs])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Backup freshness
          {staleCount > 0 && <Badge variant="danger">{staleCount} behind</Badge>}
        </CardTitle>
        <CardDescription>
          Newest restore point per namespace vs its schedule cadence (stale after 1.5× the expected
          interval).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {uncovered.length > 0 && (
          <Alert variant="warning">
            <div className="flex items-start gap-2 text-sm">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <div>
                <span className="font-medium">Namespaces with PVCs but no backup Schedule: </span>
                {uncovered.map(([ns, n], i) => (
                  <span key={ns} className="font-mono text-xs">
                    {i > 0 && ', '}
                    {ns} ({n} PVC{n === 1 ? '' : 's'})
                  </span>
                ))}
              </div>
            </div>
          </Alert>
        )}
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {loading ? 'Loading…' : 'No schedules or snapshots found.'}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Namespace</TableHead>
                <TableHead>Cadence</TableHead>
                <TableHead>Last backup</TableHead>
                <TableHead>Age</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.namespace}>
                  <TableCell className="font-mono text-xs">
                    <Link
                      to={`/snapshots?namespace=${encodeURIComponent(r.namespace)}`}
                      className="hover:underline"
                    >
                      {r.namespace}
                    </Link>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{r.cron || '—'}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {formatWhen(r.lastBackup)}
                  </TableCell>
                  <TableCell className="text-xs">{r.ageMs != null ? formatAge(r.ageMs) : '—'}</TableCell>
                  <TableCell>
                    {r.state === 'fresh' && <Badge variant="success">fresh</Badge>}
                    {r.state === 'stale' && <Badge variant="danger">stale</Badge>}
                    {r.state === 'never' && <Badge variant="danger">no backups yet</Badge>}
                    {r.state === 'no-schedule' && <Badge variant="warning">no schedule</Badge>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {pvcs === null && !loading && (
          <p className="text-xs text-muted-foreground">
            PVC coverage unavailable (could not list PersistentVolumeClaims).
          </p>
        )}
      </CardContent>
    </Card>
  )
}
