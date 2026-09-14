import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import PageHeader from '../components/PageHeader'
import { Download } from 'lucide-react'
import { api, type AuditEntry } from '../api'
import { formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader } from '../components/ui/card'
import { Input } from '../components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../components/ui/table'

const PAGE_SIZE = 50

export default function Audit() {
  const [params, setParams] = useSearchParams()
  const setParam = (key: string, value: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (value) next.set(key, value)
        else next.delete(key)
        next.delete('offset')
        return next
      },
      { replace: true },
    )
  const [items, setItems] = useState<AuditEntry[]>([])
  const [total, setTotal] = useState(0)
  const kind = params.get('kind') || ''
  const actor = params.get('actor') || ''
  const since = params.get('since') || ''
  const until = params.get('until') || ''
  const offset = Math.max(0, Number(params.get('offset')) || 0)
  const setKind = (value: string) => setParam('kind', value)
  const setActor = (value: string) => setParam('actor', value)
  const setSince = (value: string) => setParam('since', value)
  const setUntil = (value: string) => setParam('until', value)
  const setOffset = (value: number) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set('offset', String(value))
        return next
      },
      { replace: true },
    )
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  // Debounce the free-text actor filter so typing doesn't spam the API.
  const [actorInput, setActorInput] = useState(actor)
  useEffect(() => setActorInput(actor), [actor])
  useEffect(() => {
    const t = setTimeout(() => {
      if (actorInput.trim() !== actor) setActor(actorInput.trim())
    }, 350)
    return () => clearTimeout(t)
  }, [actorInput, actor])

  // Any filter change resets to the first page.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
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
        if (cancelled) return
        setItems(page.entries)
        setTotal(page.total)
        setError('')
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [kind, actor, since, until, offset])

  const page = Math.floor(offset / PAGE_SIZE) + 1
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className="space-y-6">
      <PageHeader
        title="Audit log"
        description="Trace restore operations, downloads, and backup jobs across the last 90 days."
      />
      {loading && (
        <p role="status" className="text-sm text-muted-foreground">
          Loading audit events…
        </p>
      )}
      {error && <Alert variant="danger">{error}</Alert>}
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-end gap-3 space-y-0">
          <div className="grid gap-1">
            <label htmlFor="audit-kind" className="text-xs text-muted-foreground">
              Kind
            </label>
            <select
              id="audit-kind"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              value={kind}
              onChange={(e) => setKind(e.target.value)}
            >
              <option value="">all</option>
              <option value="restore">restore</option>
              <option value="download">download</option>
              <option value="backup">backup</option>
              <option value="check">check</option>
              <option value="drill">drill</option>
              <option value="system">system</option>
            </select>
          </div>
          <div className="grid gap-1">
            <label htmlFor="audit-actor" className="text-xs text-muted-foreground">
              Actor
            </label>
            <Input
              id="audit-actor"
              placeholder="username"
              value={actorInput}
              onChange={(e) => setActorInput(e.target.value)}
              className="h-9 w-36"
            />
          </div>
          <div className="grid gap-1">
            <label htmlFor="audit-from" className="text-xs text-muted-foreground">
              From
            </label>
            <Input
              id="audit-from"
              type="date"
              value={since}
              onChange={(e) => setSince(e.target.value)}
              className="h-9 w-40"
            />
          </div>
          <div className="grid gap-1">
            <label htmlFor="audit-to" className="text-xs text-muted-foreground">
              To
            </label>
            <Input
              id="audit-to"
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
                <TableHead className="hidden md:table-cell">Actor</TableHead>
                <TableHead className="hidden sm:table-cell">Status</TableHead>
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
                  <TableCell className="hidden md:table-cell">{e.actor}</TableCell>
                  <TableCell className="hidden sm:table-cell">{e.status || '—'}</TableCell>
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
                    {loading
                      ? 'Loading events…'
                      : error
                        ? 'Audit data unavailable.'
                        : 'No entries match these filters.'}
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
