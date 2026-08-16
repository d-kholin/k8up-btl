import { useEffect, useState } from 'react'
import { api, type AuditEntry } from '../api'
import { formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

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
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground">90-day retention · restores, downloads, jobs</p>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}
      <Card>
        <CardHeader className="flex flex-row items-center gap-3 space-y-0">
          <CardTitle className="text-sm font-medium text-muted-foreground">Kind</CardTitle>
          <select
            className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            value={kind}
            onChange={(e) => setKind(e.target.value)}
          >
            <option value="">all</option>
            <option value="restore">restore</option>
            <option value="download">download</option>
            <option value="backup">backup</option>
            <option value="check">check</option>
            <option value="system">system</option>
          </select>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Detail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {formatWhen(e.at)}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">{e.kind}</Badge>
                  </TableCell>
                  <TableCell>{e.actor}</TableCell>
                  <TableCell>{e.status || '—'}</TableCell>
                  <TableCell className="max-w-md truncate font-mono text-xs text-muted-foreground">
                    {[e.namespace, e.pvc, e.snapshot?.slice(0, 12), e.path, e.argoApp, e.detail]
                      .filter(Boolean)
                      .join(' · ')}
                    {e.bytes ? ` · ${formatBytes(e.bytes)}` : ''}
                    {e.argoPaused === true ? ' · argo paused' : ''}
                  </TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground">
                    No entries yet.
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
