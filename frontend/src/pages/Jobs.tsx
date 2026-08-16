import { useEffect, useState } from 'react'
import { api, type K8sObject } from '../api'

export default function Jobs() {
  const [jobs, setJobs] = useState<Record<string, K8sObject[] | { error: string }>>({})
  const [error, setError] = useState('')
  const [ns, setNs] = useState('')

  const load = () => api.jobs().then(setJobs).catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const t = setInterval(load, 10000)
    return () => clearInterval(t)
  }, [])

  async function trigger(kind: 'backup' | 'check') {
    if (!ns) {
      setError('Enter a namespace first')
      return
    }
    if (!window.confirm(`Create on-demand ${kind} in namespace ${ns}?`)) return
    try {
      if (kind === 'backup') await api.createBackup(ns)
      else await api.createCheck(ns)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div>
      <h1>Jobs</h1>
      {error && <p className="error">{error}</p>}
      <div className="card row">
        <input placeholder="Namespace for ad-hoc job" value={ns} onChange={(e) => setNs(e.target.value)} />
        <button onClick={() => trigger('backup')}>Run Backup…</button>
        <button className="secondary" onClick={() => trigger('check')}>
          Run Check…
        </button>
      </div>
      {(['backups', 'restores', 'checks', 'prunes'] as const).map((key) => {
        const val = jobs[key]
        const list = Array.isArray(val) ? val : []
        const err = val && !Array.isArray(val) ? val.error : ''
        return (
          <div key={key} className="card">
            <h2 style={{ marginTop: 0 }}>{key}</h2>
            {err && <p className="error">{err}</p>}
            <table>
              <thead>
                <tr>
                  <th>Namespace</th>
                  <th>Name</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {list.slice(0, 50).map((j) => (
                  <tr key={`${j.namespace}/${j.name}`}>
                    <td className="mono">{j.namespace}</td>
                    <td className="mono">{j.name}</td>
                    <td className="muted">{j.creationTimestamp ? new Date(j.creationTimestamp).toLocaleString() : '—'}</td>
                  </tr>
                ))}
                {list.length === 0 && !err && (
                  <tr>
                    <td colSpan={3} className="muted">
                      None
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )
      })}
    </div>
  )
}
