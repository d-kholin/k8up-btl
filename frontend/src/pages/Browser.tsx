import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type FileNode } from '../api'
import { formatBytes } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

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
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Browse snapshot</h1>
          <p className="font-mono text-sm text-muted-foreground">
            {ns}/{name}
          </p>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to="/snapshots">← Snapshots</Link>
        </Button>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}

      <Card>
        <CardHeader className="flex flex-row items-center gap-3 space-y-0">
          <CardTitle className="text-sm font-normal text-muted-foreground">Path</CardTitle>
          <code className="text-sm">{path}</code>
          {parent && (
            <Button size="sm" variant="secondary" onClick={() => setPath(parent)}>
              ↑ Up
            </Button>
          )}
          {loading && <span className="text-xs text-muted-foreground">Loading…</span>}
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Size</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {nodes.map((n) => (
                <TableRow key={n.path || n.name}>
                  <TableCell className="font-mono text-xs">
                    {n.type === 'dir' ? (
                      <button
                        type="button"
                        className="text-primary hover:underline"
                        onClick={() => setPath(n.path || `/${n.name}`)}
                      >
                        {(n.name || n.path) + '/'}
                      </button>
                    ) : (
                      n.name || n.path
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{n.type || '—'}</TableCell>
                  <TableCell className="text-muted-foreground">{formatBytes(n.size)}</TableCell>
                  <TableCell className="text-right">
                    {n.type !== 'dir' && (
                      <a
                        className="text-sm text-primary hover:underline"
                        href={api.downloadUrl(ns, name, n.path || n.name)}
                        download
                      >
                        Download
                      </a>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {!loading && nodes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    Empty or unavailable.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
