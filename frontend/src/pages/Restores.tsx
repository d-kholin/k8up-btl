import { useEffect, useMemo, useRef, useState } from 'react'
import { api, type RestoreState } from '../api'
import { cn, formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

const MAX_LINES = 1500

export default function Restores() {
  const [items, setItems] = useState<RestoreState[]>([])
  const [error, setError] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [logs, setLogs] = useState<Record<string, string[]>>({})
  const logEndRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)

  const load = () =>
    api
      .restores()
      .then((list) => {
        setItems(list)
        // Auto-select newest active restore
        setSelectedId((cur) => {
          if (cur && list.some((r) => r.restoreId === cur)) return cur
          const active = list.find((r) => !['done', 'failed'].includes(r.step))
          return active?.restoreId || list[0]?.restoreId || null
        })
      })
      .catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const es = new EventSource('/api/v1/events')
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as {
          type?: string
          restoreId?: string
          line?: string
          data?: RestoreState
        }
        if (msg.type === 'restore') {
          load()
          return
        }
        if (msg.type === 'restore-log' && msg.restoreId && msg.line != null) {
          setLogs((prev) => {
            const prevLines = prev[msg.restoreId!] || []
            const next = [...prevLines, msg.line!]
            return {
              ...prev,
              [msg.restoreId!]: next.length > MAX_LINES ? next.slice(-MAX_LINES) : next,
            }
          })
          if (!selectedId) setSelectedId(msg.restoreId)
        }
      } catch {
        load()
      }
    }
    const t = setInterval(load, 8000)
    return () => {
      es.close()
      clearInterval(t)
    }
  }, [])

  // Catch-up buffer when selection changes
  useEffect(() => {
    if (!selectedId) return
    api
      .restoreLogs(selectedId)
      .then((res) => {
        setLogs((prev) => {
          const live = prev[selectedId] || []
          // merge: prefer longer buffer
          const merged =
            res.lines.length >= live.length ? res.lines : [...res.lines, ...live.slice(res.lines.length)]
          const uniq = dedupeTail(merged)
          return { ...prev, [selectedId]: uniq.slice(-MAX_LINES) }
        })
      })
      .catch(() => {
        /* ignore missing */
      })
  }, [selectedId])

  useEffect(() => {
    if (stickBottom.current) {
      logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [logs, selectedId])

  const selected = useMemo(
    () => items.find((r) => r.restoreId === selectedId) || null,
    [items, selectedId],
  )
  const lines = selectedId ? logs[selectedId] || [] : []

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-hidden">
      <div className="shrink-0">
        <h1 className="text-2xl font-semibold tracking-tight">Restore operations</h1>
        <p className="text-sm text-muted-foreground">
          Live step status and K8up/restic job logs while a restore runs
        </p>
      </div>
      {error && (
        <div className="shrink-0">
          <Alert variant="danger">{error}</Alert>
        </div>
      )}

      <div className="grid min-h-0 flex-1 gap-4 overflow-hidden lg:grid-cols-5 lg:items-stretch">
        <Card className="flex min-h-0 flex-col overflow-hidden lg:col-span-2">
          <CardHeader className="shrink-0">
            <CardTitle>Jobs</CardTitle>
            <CardDescription>Select a restore to view logs</CardDescription>
          </CardHeader>
          <CardContent className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>Step</TableHead>
                  <TableHead>PVC</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((r) => (
                  <TableRow
                    key={r.restoreId}
                    className={cn(
                      'cursor-pointer',
                      selectedId === r.restoreId && 'bg-muted/60',
                    )}
                    onClick={() => setSelectedId(r.restoreId)}
                  >
                    <TableCell className="font-mono text-xs">{r.restoreId.slice(0, 8)}</TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          r.step === 'done'
                            ? 'success'
                            : r.step === 'failed'
                              ? 'danger'
                              : 'warning'
                        }
                      >
                        {r.step}
                        {r.step === 'restoring' && r.progressPercent != null
                          ? ` ${Math.round(r.progressPercent)}%`
                          : ''}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-[140px] truncate font-mono text-xs">
                      {r.kind === 'recovery'
                        ? `${r.pvcNamespace} · SQL (${r.dbPod || 'db'})`
                        : `${r.pvcNamespace}/${r.pvcName}`}
                    </TableCell>
                  </TableRow>
                ))}
                {items.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={3} className="text-muted-foreground">
                      No restore jobs yet.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <Card className="flex min-h-0 flex-col overflow-hidden lg:col-span-3">
          <CardHeader className="flex shrink-0 flex-row flex-wrap items-start justify-between gap-2 space-y-0">
            <div>
              <CardTitle>Live log</CardTitle>
              <CardDescription>
                {selected ? (
                  <>
                    <span className="font-mono text-foreground">
                      {selected.restoreId.slice(0, 8)}
                    </span>
                    {' · '}
                    {selected.step}
                    {' · '}
                    {formatWhen(selected.startedAt)}
                    {selected.restoreCRName ? ` · CR ${selected.restoreCRName}` : ''}
                  </>
                ) : (
                  'No restore selected'
                )}
              </CardDescription>
            </div>
            <div className="flex gap-2">
              {selected && !['done', 'failed'].includes(selected.step) && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={selected.cancelRequested}
                  onClick={() => {
                    if (!window.confirm('Cancel this restore? The volume may be left with a partial restore.')) return
                    api
                      .cancelRestore(selected.restoreId)
                      .then(load)
                      .catch((e: Error) => setError(e.message))
                  }}
                >
                  {selected.cancelRequested ? 'Cancelling…' : 'Cancel restore'}
                </Button>
              )}
              {selected?.argoSyncResumed === false && (
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() =>
                    api
                      .resumeArgo('argocd', 'application-controller')
                      .then(load)
                      .catch((e: Error) => setError(e.message))
                  }
                >
                  Resume Argo controller
                </Button>
              )}
            </div>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col overflow-hidden">
            {selected?.lastError && (
              <Alert variant="danger" className="mb-3 shrink-0">
                {selected.lastError}
              </Alert>
            )}
            {selected &&
              selected.step === 'restoring' &&
              selected.progressPercent != null && (
                <div className="mb-3 shrink-0">
                  <div className="mb-1 flex justify-between text-xs text-muted-foreground">
                    <span>restic progress (from job logs)</span>
                    <span>{Math.round(selected.progressPercent)}%</span>
                  </div>
                  <div className="h-2 overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-primary transition-all"
                      style={{ width: `${Math.min(100, selected.progressPercent)}%` }}
                    />
                  </div>
                </div>
              )}
            {/* Independent scrollport: fills remaining card height; does not grow the page */}
            <div
              className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-md border bg-[hsl(var(--log-bg))] p-3 font-mono text-[11px] leading-relaxed text-[hsl(var(--log-fg))] [overflow-anchor:none]"
              onWheel={(e) => {
                // Keep wheel inside this pane even when main/page would otherwise chain-scroll.
                e.stopPropagation()
              }}
              onScroll={(e) => {
                const el = e.currentTarget
                stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
              }}
            >
              {lines.length === 0 ? (
                <div className="text-muted-foreground">
                  {selected
                    ? 'Waiting for log lines… (orchestrator steps + restore Job pod)'
                    : 'Pick a restore job.'}
                </div>
              ) : (
                lines.map((line, i) => (
                  <div key={i} className="whitespace-pre-wrap break-all">
                    {line}
                  </div>
                ))
              )}
              <div ref={logEndRef} />
            </div>
            {selected && (
              <div className="mt-3 flex shrink-0 flex-wrap gap-2 text-xs text-muted-foreground">
                {selected.step === 'done' && selected.argoSyncResumed !== false && (
                  <Badge variant="success">complete, Argo resumed</Badge>
                )}
                {selected.step === 'failed' && selected.cancelled && (
                  <Badge variant="warning">cancelled by operator</Badge>
                )}
                {selected.step === 'failed' && !selected.cancelled && selected.argoSyncResumed && (
                  <Badge variant="warning">failed, Argo resumed</Badge>
                )}
                {selected.step === 'failed' && selected.argoSyncResumed === false && (
                  <Badge variant="danger">failed, Argo still stopped</Badge>
                )}
                {selected.step === 'done' && !!selected.bytesRecovered && (
                  <span>{formatBytes(selected.bytesRecovered)} restored</span>
                )}
                {selected.kind === 'recovery' && !!selected.pvcParts?.length && (
                  <span>
                    PVCs:{' '}
                    {selected.pvcParts
                      .map((p) => `${p.pvcName} ${p.status === 'done' ? '✓' : p.status || ''}`)
                      .join(', ')}
                  </span>
                )}
                {selected.kind === 'recovery' && selected.safetyBackupCR && (
                  <span>safety backup {selected.safetyBackupCR}</span>
                )}
                <span>{lines.length} lines</span>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function dedupeTail(lines: string[]): string[] {
  // Keep order; drop exact consecutive duplicates only
  const out: string[] = []
  for (const l of lines) {
    if (out.length === 0 || out[out.length - 1] !== l) out.push(l)
  }
  return out
}
