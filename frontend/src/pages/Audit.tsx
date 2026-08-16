import { useEffect, useState } from 'react'
import { api, type AuditEntry } from '../api'

export default function Audit() {
  const [items, setItems] = useState<AuditEntry[]>([])
  const [kind, setKind] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .audit(kind || undefined)
      .then(setItems)
      .catch((e: Error) => setError(e.message))
  }, [kind])

  return (
    <div>
      <h1>Audit log</h1>
      <p className="muted">90-day retention (restores + file downloads + ad-hoc jobs).</p>
      {error && <p className="error">{error}</p>}
      <div className="card row">
        <label className="muted">
          Kind{' '}
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">all</option>
            <option value="restore">restore</option>
            <option value="download">download</option>
            <option value="backup">backup</option>
            <option value="check">check</option>
            <option value="system">system</option>
          </select>
        </label>
      </div>
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>When</th>
              <th>Kind</th>
              <th>Actor</th>
              <th>Status</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td className="muted">{new Date(e.at).toLocaleString()}</td>
                <td>
                  <span className="badge">{e.kind}</span>
                </td>
                <td>{e.actor}</td>
                <td>{e.status || '—'}</td>
                <td className="mono">
                  {[e.namespace, e.pvc, e.snapshot, e.path, e.argoApp, e.detail].filter(Boolean).join(' · ')}
                  {e.argoPaused === true && <span className="badge bad"> argo paused</span>}
                  {e.bytes ? ` · ${e.bytes} B` : ''}
                </td>
              </tr>
            ))}
            {items.length === 0 && (
              <tr>
                <td colSpan={5} className="muted">
                  No entries yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
