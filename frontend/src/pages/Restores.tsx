import { useEffect, useState } from 'react'
import { api, type RestoreState } from '../api'

export default function Restores() {
  const [items, setItems] = useState<RestoreState[]>([])
  const [error, setError] = useState('')

  const load = () => api.restores().then(setItems).catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const es = new EventSource('/api/v1/events')
    es.onmessage = () => {
      load()
    }
    const t = setInterval(load, 5000)
    return () => {
      es.close()
      clearInterval(t)
    }
  }, [])

  return (
    <div>
      <h1>Restore operations</h1>
      {error && <p className="error">{error}</p>}
      <div className="card">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Step</th>
              <th>PVC</th>
              <th>Argo</th>
              <th>Result</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {items.map((r) => (
              <tr key={r.restoreId}>
                <td className="mono">{r.restoreId.slice(0, 8)}</td>
                <td>
                  <span className={`badge ${stepClass(r)}`}>{r.step}</span>
                </td>
                <td className="mono">
                  {r.pvcNamespace}/{r.pvcName}
                </td>
                <td className="mono">
                  {r.application ? `${r.application.namespace}/${r.application.name}` : '—'}
                </td>
                <td>
                  {r.step === 'done' && r.argoSyncResumed !== false && (
                    <span className="badge ok">complete, Argo resumed</span>
                  )}
                  {r.step === 'failed' && r.argoSyncResumed && (
                    <span className="badge warn">failed, Argo resumed</span>
                  )}
                  {r.step === 'failed' && r.argoSyncResumed === false && (
                    <span className="badge bad">failed, Argo controller still stopped</span>
                  )}
                  {r.lastError && <div className="error">{r.lastError}</div>}
                </td>
                <td>
                  {r.argoSyncResumed === false && (
                    <button
                      className="danger"
                      onClick={() =>
                        api
                          .resumeArgo('argocd', 'application-controller')
                          .then(load)
                          .catch((e: Error) => setError(e.message))
                      }
                    >
                      Resume Argo controller
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {items.length === 0 && (
              <tr>
                <td colSpan={6} className="muted">
                  No restore jobs in this backend process yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function stepClass(r: RestoreState) {
  if (r.step === 'done') return 'ok'
  if (r.step === 'failed') return 'bad'
  return 'warn'
}
