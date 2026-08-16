import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { api, type FileNode } from '../api'

export default function Browser() {
  const { ns = '', name = '' } = useParams()
  const [path, setPath] = useState('/')
  const [nodes, setNodes] = useState<FileNode[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    setError('')
    api
      .files(ns, name, path)
      .then(setNodes)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [ns, name, path])

  const parent = path === '/' ? null : path.replace(/\/?[^/]+\/?$/, '') || '/'

  return (
    <div>
      <h1>
        Browse <span className="mono">{ns}/{name}</span>
      </h1>
      <div className="card row">
        <span className="muted">Path:</span>
        <span className="mono">{path}</span>
        {parent && (
          <button className="secondary" onClick={() => setPath(parent)}>
            ↑ Up
          </button>
        )}
      </div>
      {error && <p className="error">{error}</p>}
      {loading && <p className="muted">Loading…</p>}
      <div className="card file-tree">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Type</th>
              <th>Size</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {nodes.map((n) => (
              <tr key={n.path || n.name}>
                <td className="mono">
                  {n.type === 'dir' ? (
                    <button className="linkish" onClick={() => setPath(n.path || `/${n.name}`)}>
                      {n.name || n.path}/
                    </button>
                  ) : (
                    n.name || n.path
                  )}
                </td>
                <td>{n.type || '—'}</td>
                <td className="muted">{n.size != null ? formatBytes(n.size) : '—'}</td>
                <td>
                  {n.type !== 'dir' && (
                    <a href={api.downloadUrl(ns, name, n.path || n.name)} download>
                      Download
                    </a>
                  )}
                </td>
              </tr>
            ))}
            {!loading && nodes.length === 0 && (
              <tr>
                <td colSpan={4} className="muted">
                  Empty or unavailable (check restic secret mapping).
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function formatBytes(n: number) {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < u.length - 1)
  return `${v.toFixed(1)} ${u[i]}`
}
