import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type K8sObject, type RestoreState } from '../api'

export default function Dashboard() {
  const [schedules, setSchedules] = useState<K8sObject[]>([])
  const [interrupted, setInterrupted] = useState<RestoreState[]>([])
  const [meta, setMeta] = useState<{ grafanaDashboardUrl?: string } | null>(null)
  const [error, setError] = useState('')

  const load = () => {
    Promise.all([api.schedules(), api.interrupted(), api.meta()])
      .then(([s, i, m]) => {
        setSchedules(s)
        setInterrupted(i)
        setMeta(m)
      })
      .catch((e: Error) => setError(e.message))
  }

  useEffect(() => {
    load()
  }, [])

  return (
    <div>
      <h1>Backup dashboard</h1>
      {error && <p className="error">{error}</p>}

      {interrupted.length > 0 && (
        <div className="banner">
          <strong>Interrupted restore — Argo CD controller may still be stopped.</strong>
          <p className="muted" style={{ margin: '0.35rem 0' }}>
            Restores pause the whole cluster&apos;s Argo reconciliation (
            <span className="mono">argocd-application-controller</span>) until finished.
          </p>
          <ul>
            {interrupted.map((r) => (
              <li key={r.restoreId}>
                {r.application
                  ? `${r.application.namespace}/${r.application.name}`
                  : 'global pause'}{' '}
                — step {r.step}
                {r.lastError && <div className="error">{r.lastError}</div>}
                {' '}
                <button
                  type="button"
                  className="secondary"
                  onClick={() =>
                    api
                      .resumeArgo('argocd', 'application-controller')
                      .then(load)
                      .catch((e: Error) => setError(e.message))
                  }
                >
                  Resume Argo controller
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="card row">
        <span className="muted">Schedules:</span>
        <strong>{schedules.length}</strong>
        <Link to="/snapshots">Browse snapshots →</Link>
        <Link to="/restores">Restore jobs →</Link>
        {meta?.grafanaDashboardUrl && (
          <a href={meta.grafanaDashboardUrl} target="_blank" rel="noreferrer">
            K8up Grafana dashboard ↗
          </a>
        )}
      </div>

      <h2>Schedules</h2>
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>Namespace</th>
              <th>Name</th>
              <th>Schedule</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {schedules.map((s) => (
              <tr key={`${s.namespace}/${s.name}`}>
                <td className="mono">{s.namespace}</td>
                <td className="mono">{s.name}</td>
                <td className="mono">{String((s.spec as { backup?: { schedule?: string } })?.backup?.schedule ?? '—')}</td>
                <td>
                  <span className="badge">{s.status ? 'present' : 'unknown'}</span>
                </td>
              </tr>
            ))}
            {schedules.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  No Schedule CRs found (or API not connected to cluster).
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
