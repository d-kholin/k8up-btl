import { useEffect, useState } from 'react'
import { Download } from 'lucide-react'
import { api, type AuditEntry } from '../api'
import { formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader } from '../components/ui/card'
import { Input } from '../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

const PAGE_SIZE = 50

export default function Audit() {
  const [items, setItems] = useState<AuditEntry[]>([])
  const [total, setTotal] = useState(0)
  const [kind, setKind] = useState('')
  const [actor, setActor] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const [offset, setOffset] = useState(0)
  const [error, setError] = useState('')

  // Debounce the free-text actor filter so typing doesn't spam the API.
  const [actorInput, setActorInput] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setActor(actorInput.trim()), 350)
    return () => clearTimeout(t)
  }, [actorInput])

  // Any filter change resets to the first page.
  useEffect(() => {
    setOffset(0)
  }, [kind, actor, since, until])

  useEffect(() => {
    api
      .audit({
        kind: kind || undefined,
        actor: actor || undefined,
        since: since || undefined,
        until: until || undefined,
        limit: PAGE_SIZE,
        offset,
      })
      .then((page) => {
        setItems(page.entries)
        setTotal(page.total)
        setError('')
      })
      .catch((e: Error) => setError(e.message))
  }, [kind, actor, since, until, offset])

  const page = Math.floor(offset / PAGE_SIZE) + 1
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground">90-day retention · restores, downloads, jobs</p>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-end gap-3 space-y-0">
          <div className="grid gap-1">
            <label className="text-xs text-muted-foreground">Kind</label>
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
          </div>
          <div className="grid gap-1">
            <label className="text-xs text-muted-foreground">Actor</label>
            <Input
              placeholder="username"
              value={actorInput}
              onChange={(e) => setActorInput(e.target.value)}
              className="h-9 w-36"
            />
          </div>
          <div className="grid gap-1">
            <label className="text-xs text-muted-foreground">From</label>
            <Input
              type="date"
              value={since}
              onChange={(e) => setSince(e.target.value)}
              className="h-9 w-40"
            />
          </div>
          <div className="grid gap-1">
            <label className="text-xs text-muted-foreground">To</label>
            <Input
              type="date"
              value={until}
              onChange={(e) => setUntil(e.target.value)}
              className="h-9 w-40"
            />
          </div>
          <div className="ml-auto flex items-center gap-3">
            <span className="text-sm text-muted-foreground">{total} entries</span>
            <Button asChild size="sm" variant="outline">
              <a
                href={api.auditExportUrl({
                  kind: kind || undefined,
                  actor: actor || undefined,
                  since: since || undefined,
                  until: until || undefined,
                })}
                download
              >
                <Download className="h-3.5 w-3.5" />
                Export CSV
              </a>
            </Button>
          </div>
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
                    No entries match.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <div className="mt-4 flex items-center justify-between text-sm text-muted-foreground">
            <span>
              Page {page} of {pages}
            </span>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              >
                Previous
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset(offset + PAGE_SIZE)}
              >
                Next
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
