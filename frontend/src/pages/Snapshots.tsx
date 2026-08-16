import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type K8sObject } from '../api'

export default function Snapshots() {
  const [items, setItems] = useState<K8sObject[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState<string | null>(null)

  useEffect(() => {
    api.snapshots().then(setItems).catch((e: Error) => setError(e.message))
  }, [])

  const filtered = items.filter((s) => {
    if (!filter) return true
    const q = filter.toLowerCase()
    return (
      (s.namespace || '').toLowerCase().includes(q) ||
      (s.name || '').toLowerCase().includes(q)
    )
  })

  async function restore(s: K8sObject) {
    const pvcNamespace = window.prompt('Target PVC namespace', s.namespace || '') || ''
    const pvcName = window.prompt('Target PVC name', '') || ''
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

  return (
    <div>
      <h1>Snapshots</h1>
      {error && <p className="error">{error}</p>}
      <div className="card row">
        <input
          placeholder="Filter namespace/name…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{ minWidth: 260 }}
        />
        <span className="muted">{filtered.length} shown</span>
      </div>
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>Namespace</th>
              <th>Name</th>
              <th>Created</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((s) => (
              <tr key={`${s.namespace}/${s.name}`}>
                <td className="mono">{s.namespace}</td>
                <td className="mono">{s.name}</td>
                <td className="muted">{s.creationTimestamp ? new Date(s.creationTimestamp).toLocaleString() : '—'}</td>
                <td className="row">
                  <Link to={`/snapshots/${s.namespace}/${s.name}/browse`}>Browse files</Link>
                  <button disabled={busy === `${s.namespace}/${s.name}`} onClick={() => restore(s)}>
                    Restore…
                  </button>
                </td>
              </tr>
            ))}
            {filtered.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  No snapshots.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
