import type { K8sObject, PVCRef } from '../api'
import { snapSpec, snapTime, sourcePvcCandidates } from './snapshots'
import { cronIntervalMs } from './utils'

// Only classify cadence when the expression has a predictable interval.
// Complex calendars must not silently become a daily/weekly green status.
export function coverageInterval(cron?: string): number | null {
  if (!cron) return null
  if (/^@(hourly|daily|weekly|monthly|yearly|annually|midnight)(-random)?$/.test(cron))
    return cronIntervalMs(cron)
  const fields = cron.trim().split(/\s+/)
  if (fields.length !== 5 || fields.slice(2).some((f) => f !== '*')) return null
  const [minute, hour] = fields
  const numeric = (value: string, max: number) => /^\d+$/.test(value) && Number(value) <= max
  if (hour === '*') {
    if (minute === '*') return 60_000
    const step = minute.match(/^\*\/(\d+)$/)
    if (step && Number(step[1]) > 0 && Number(step[1]) <= 59) return Number(step[1]) * 60_000
    return numeric(minute, 59) ? 3_600_000 : null
  }
  if (!numeric(minute, 59)) return null
  const step = hour.match(/^\*\/(\d+)$/)
  if (step && Number(step[1]) > 0 && Number(step[1]) <= 23) return Number(step[1]) * 3_600_000
  return numeric(hour, 23) ? 86_400_000 : null
}

export type CoverageState = 'fresh' | 'stale' | 'missing' | 'unknown' | 'excluded' | 'unscheduled'
export type CoverageResource = {
  namespace: string
  name: string
  kind: 'PVC' | 'SQL dump'
  state: CoverageState
  latest?: number
  reason: string
}

// Snapshot evidence is per source, never inferred from another volume in its namespace.
// Observed dumps are included; discovering dumps that have never run needs pod inventory.
export function coverageResources(
  schedules: K8sObject[],
  snapshots: K8sObject[],
  pvcs: PVCRef[] | null,
  now = Date.now(),
): CoverageResource[] {
  const cadences = new Map<string, (number | null)[]>()
  for (const s of schedules) {
    const cron = (s.spec?.backup as { schedule?: string } | undefined)?.schedule
    const ns = s.namespace || 'default'
    cadences.set(ns, [...(cadences.get(ns) || []), coverageInterval(cron)])
  }
  const resources = new Map<string, CoverageResource>()
  const key = (ns: string, kind: string, name: string) => `${ns}/${kind}/${name}`
  for (const pvc of pvcs || [])
    resources.set(key(pvc.namespace, 'PVC', pvc.name), {
      namespace: pvc.namespace,
      name: pvc.name,
      kind: 'PVC',
      state: pvc.backupExcluded ? 'excluded' : 'missing',
      reason: pvc.backupExcluded
        ? 'Excluded by k8up.io/backup=false'
        : 'No matching snapshot found',
    })
  for (const snapshot of snapshots) {
    const ns = snapshot.namespace || 'default'
    const sources = [
      ...sourcePvcCandidates(snapshot).map((name) => ({
        name,
        kind: 'PVC' as const,
      })),
      ...(snapSpec(snapshot).paths || [])
        .filter((p) => p.endsWith('.sql'))
        .map((name) => ({ name, kind: 'SQL dump' as const })),
    ]
    for (const source of sources) {
      const id = key(ns, source.kind, source.name)
      const row = resources.get(id) || {
        namespace: ns,
        ...source,
        state: 'unknown' as const,
        reason: '',
      }
      const time = snapTime(snapshot)
      if (Number.isFinite(time) && time > 0 && (!row.latest || time > row.latest)) row.latest = time
      resources.set(id, row)
    }
  }
  for (const row of resources.values()) {
    if (row.state === 'excluded') continue
    if (!row.latest) {
      row.state = 'missing'
      row.reason = 'No dated snapshot found'
      continue
    }
    const intervals = cadences.get(row.namespace)
    if (!intervals?.length) {
      row.state = 'unscheduled'
      row.reason = 'Snapshot exists, but no schedule is configured'
      continue
    }
    // Several schedules cannot be attributed reliably to individual resources from this API.
    if (intervals.length !== 1 || intervals[0] === null || row.latest > now) {
      row.state = 'unknown'
      row.reason =
        row.latest > now
          ? 'Snapshot timestamp is in the future'
          : 'Backup cadence cannot be attributed reliably'
      continue
    }
    row.state = now - row.latest > intervals[0] * 1.5 ? 'stale' : 'fresh'
    row.reason =
      row.state === 'fresh'
        ? 'Within 1.5× the namespace backup interval'
        : 'Older than 1.5× the namespace backup interval'
  }
  const weight: Record<CoverageState, number> = {
    missing: 0,
    stale: 1,
    unknown: 2,
    unscheduled: 3,
    fresh: 4,
    excluded: 5,
  }
  return [...resources.values()].sort(
    (a, b) =>
      weight[a.state] - weight[b.state] ||
      a.namespace.localeCompare(b.namespace) ||
      a.name.localeCompare(b.name),
  )
}
