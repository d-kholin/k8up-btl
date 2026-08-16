import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type K8sObject } from '../api'

type SnapSpec = {
  date?: string
  id?: string
  paths?: string[]
  repository?: string
}

function snapSpec(s: K8sObject): SnapSpec {
  return (s.spec || {}) as SnapSpec
}

/** Workload/PVC key from restic paths, e.g. /data/lubelogger-data-encrypted or /mealie-postgres.sql */
function workloadKey(s: K8sObject): string {
  const paths = snapSpec(s).paths || []
  if (paths.length === 0) return '(unknown)'
  return paths
    .map((p) => {
      const cleaned = p.replace(/\/+$/, '')
      const base = cleaned.split('/').filter(Boolean).pop() || cleaned
      return base
    })
    .join(', ')
}

function snapTime(s: K8sObject): number {
  const d = snapSpec(s).date || s.creationTimestamp
  return d ? new Date(d).getTime() : 0
}

type Group = {
  namespace: string
  workload: string
  key: string
  items: K8sObject[]
}

export default function Snapshots() {
  const [items, setItems] = useState<K8sObject[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  useEffect(() => {
    api.snapshots().then(setItems).catch((e: Error) => setError(e.message))
  }, [])

  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const filtered = items.filter((s) => {
      if (!q) return true
      const spec = snapSpec(s)
      const hay = [
        s.namespace,
        s.name,
        workloadKey(s),
        ...(spec.paths || []),
        spec.id,
        spec.repository,
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })

    const map = new Map<string, Group>()
    for (const s of filtered) {
      const ns = s.namespace || 'default'
      const wl = workloadKey(s)
      const key = `${ns}::${wl}`
      let g = map.get(key)
      if (!g) {
        g = { namespace: ns, workload: wl, key, items: [] }
        map.set(key, g)
      }
      g.items.push(s)
    }

    const out = Array.from(map.values())
    for (const g of out) {
      g.items.sort((a, b) => snapTime(b) - snapTime(a))
    }
    out.sort((a, b) => {
      const ns = a.namespace.localeCompare(b.namespace)
      if (ns !== 0) return ns
      return a.workload.localeCompare(b.workload)
    })
    return out
  }, [items, filter])

  function toggle(key: string) {
    setCollapsed((c) => ({ ...c, [key]: !c[key] }))
  }

  function expandAll() {
    setCollapsed({})
  }

  function collapseAll() {
    const next: Record<string, boolean> = {}
    for (const g of groups) next[g.key] = true
    setCollapsed(next)
  }

  async function restore(s: K8sObject) {
    const paths = snapSpec(s).paths || []
    const guessedPvc =
      paths
        .map((p) => p.replace(/\/+$/, '').split('/').filter(Boolean).pop() || '')
        .find((b) => b && !b.endsWith('.sql')) || ''

    const pvcNamespace = window.prompt('Target PVC namespace', s.namespace || '') || ''
    const pvcName = window.prompt('Target PVC name', guessedPvc) || ''
    if (!pvcNamespace || !pvcName) return
    const ok = window.confirm(
      `Restore snapshot ${s.namespace}/${s.name} onto PVC ${pvcNamespace}/${pvcName}?\n\nThis will scale down the owning workload, pause Argo if managed, run K8up Restore, then scale up and resume sync.`,
    )
    if (!ok) return
    setBusy(`${s.namespace}/${s.name}`)
    setError('')
    try {
      await api.startRestore({
        snapshotNamespace: s.namespace || '',
        snapshotName: s.name || '',
        pvcNamespace,
        pvcName,
      })
      window.location.href = '/restores'
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  const totalShown = groups.reduce((n, g) => n + g.items.length, 0)

  return (
    <div>
      <h1>Snapshots</h1>
      {error && <p className="error">{error}</p>}
      <div className="card row">
        <input
          placeholder="Filter namespace, PVC, path…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{ minWidth: 280 }}
        />
        <span className="muted">
          {groups.length} groups · {totalShown} snapshots
        </span>
        <button type="button" className="secondary" onClick={expandAll}>
          Expand all
        </button>
        <button type="button" className="secondary" onClick={collapseAll}>
          Collapse all
        </button>
      </div>

      {groups.length === 0 && (
        <div className="card muted">No snapshots match.</div>
      )}

      {groups.map((g) => {
        const isCollapsed = !!collapsed[g.key]
        return (
          <div className="card" key={g.key} style={{ paddingTop: '0.75rem' }}>
            <button
              type="button"
              className="linkish"
              onClick={() => toggle(g.key)}
              style={{
                display: 'flex',
                width: '100%',
                alignItems: 'baseline',
                gap: '0.75rem',
                textAlign: 'left',
                marginBottom: isCollapsed ? 0 : '0.5rem',
              }}
            >
              <span className="muted" style={{ width: '1.2rem' }}>
                {isCollapsed ? '▸' : '▾'}
              </span>
              <span>
                <span className="mono">{g.namespace}</span>
                <span className="muted"> / </span>
                <strong className="mono">{g.workload}</strong>
              </span>
              <span className="badge">{g.items.length}</span>
              <span className="muted" style={{ marginLeft: 'auto', fontSize: '0.85rem' }}>
                latest {g.items[0] ? new Date(snapTime(g.items[0])).toLocaleString() : '—'}
              </span>
            </button>

            {!isCollapsed && (
              <table>
                <thead>
                  <tr>
                    <th>When</th>
                    <th>Snapshot</th>
                    <th>Paths</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {g.items.map((s) => {
                    const spec = snapSpec(s)
                    const when = spec.date || s.creationTimestamp
                    return (
                      <tr key={`${s.namespace}/${s.name}`}>
                        <td className="muted">{when ? new Date(when).toLocaleString() : '—'}</td>
                        <td className="mono">
                          <div>{s.name}</div>
                          {spec.id && (
                            <div className="muted" style={{ fontSize: '0.75rem' }} title={spec.id}>
                              {spec.id.slice(0, 12)}…
                            </div>
                          )}
                        </td>
                        <td className="mono muted" style={{ fontSize: '0.85rem' }}>
                          {(spec.paths || []).join(', ') || '—'}
                        </td>
                        <td className="row">
                          <Link to={`/snapshots/${s.namespace}/${s.name}/browse`}>Browse</Link>
                          <button
                            type="button"
                            disabled={busy === `${s.namespace}/${s.name}`}
                            onClick={() => restore(s)}
                          >
                            Restore…
                          </button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        )
      })}
    </div>
  )
}
