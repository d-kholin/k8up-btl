import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type RestoreState } from '../api'
import { cn, formatBytes, formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../components/ui/table'
import PageHeader from '../components/PageHeader'
import ConfirmAction from '../components/ConfirmAction'
import RestoreTimeline, { restoreStepLabel } from '../components/RestoreTimeline'
import { Input } from '../components/ui/input'

const MAX_LINES = 1500

export default function Restores() {
  const [params, setParams] = useSearchParams()
  const filter = params.get('q') || ''
  const [items, setItems] = useState<RestoreState[]>([])
  const [error, setError] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(params.get('id'))
  const [cancelTarget, setCancelTarget] = useState<RestoreState | null>(null)
  const [loading, setLoading] = useState(true)
  const [logError, setLogError] = useState('')
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    if (params.get('id')) setSelectedId(params.get('id'))
  }, [params])
  const selectRestore = (id: string) => {
    setSelectedId(id)
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set('id', id)
        return next
      },
      { replace: true },
    )
  }
  const [logs, setLogs] = useState<Record<string, string[]>>({})
  const logBoxRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)

  const load = () =>
    api
      .restores()
      .then((list) => {
        setError('')
        setItems(list)
        // Auto-select newest active restore
        setSelectedId((cur) => {
          if (cur && list.some((r) => r.restoreId === cur)) return cur
          const active = list.find((r) => !['done', 'failed'].includes(r.step))
          return active?.restoreId || list[0]?.restoreId || null
        })
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    const es = new EventSource('/api/v1/events')
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
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
          setSelectedId((cur) => cur || msg.restoreId || null)
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
    let cancelled = false
    setLogError('')
    api
      .restoreLogs(selectedId)
      .then((res) => {
        if (cancelled) return
        setLogs((prev) => {
          const live = prev[selectedId] || []
          // merge: prefer longer buffer
          const merged =
            res.lines.length >= live.length
              ? res.lines
              : [...res.lines, ...live.slice(res.lines.length)]
          const uniq = dedupeTail(merged)
          return { ...prev, [selectedId]: uniq.slice(-MAX_LINES) }
        })
      })
      .catch((e: Error) => {
        if (!cancelled) setLogError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [selectedId])

  // Re-pin to the tail when switching jobs, then keep the LOG BOX (never the
  // page) pinned while new lines stream in — scrollIntoView would drag every
  // scrollable ancestor down with it.
  useEffect(() => {
    stickBottom.current = true
  }, [selectedId])

  useEffect(() => {
    const el = logBoxRef.current
    if (stickBottom.current && el) el.scrollTop = el.scrollHeight
  }, [logs, selectedId])

  const selected = useMemo(
    () => items.find((r) => r.restoreId === selectedId) || null,
    [items, selectedId],
  )
  const lines = selectedId ? logs[selectedId] || [] : []

  return (
    <div className="flex flex-col gap-4 lg:h-full lg:min-h-0 lg:overflow-hidden">
      <PageHeader
        title="Restore operations"
        description="Follow recovery progress and confirm workloads and Argo CD have resumed."
        actions={
          <Button asChild variant="outline">
            <Link to="/snapshots">Find a restore point</Link>
          </Button>
        }
      />
      {error && (
        <div className="shrink-0">
          <Alert variant="danger">{error}</Alert>
        </div>
      )}

      <div className="grid gap-4 lg:min-h-0 lg:flex-1 lg:grid-cols-5 lg:items-stretch lg:overflow-hidden">
        <Card className="flex flex-col lg:col-span-2 lg:min-h-0 lg:overflow-hidden">
          <CardHeader className="shrink-0">
            <CardTitle>Jobs</CardTitle>
            <CardDescription>Select an operation to inspect its progress</CardDescription>
            <Input
              aria-label="Search restore history"
              placeholder="Search namespace, volume, or ID…"
              value={filter}
              onChange={(e) =>
                setParams(
                  (prev) => {
                    const next = new URLSearchParams(prev)
                    if (e.target.value) next.set('q', e.target.value)
                    else next.delete('q')
                    return next
                  },
                  { replace: true },
                )
              }
            />
          </CardHeader>
          <CardContent className="max-h-72 overflow-y-auto overscroll-contain lg:max-h-none lg:min-h-0 lg:flex-1">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Operation</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items
                  .filter((r) =>
                    [r.restoreId, r.pvcName, r.pvcNamespace, r.dbPod, r.application?.name]
                      .join(' ')
                      .toLowerCase()
                      .includes(filter.toLowerCase()),
                  )
                  .map((r) => (
                    <TableRow
                      key={r.restoreId}
                      className={cn('cursor-pointer', selectedId === r.restoreId && 'bg-muted/60')}
                      onClick={() => selectRestore(r.restoreId)}
                    >
                      <TableCell>
                        <button
                          type="button"
                          className="max-w-full break-words text-left font-medium text-primary"
                          aria-pressed={selectedId === r.restoreId}
                          onClick={() => selectRestore(r.restoreId)}
                        >
                          {r.kind === 'recovery'
                            ? (r.application?.name || r.pvcNamespace) + ' · SQL recovery'
                            : r.pvcName || r.application?.name || 'Volume restore'}
                        </button>
                        <p className="mt-1 text-xs text-muted-foreground">
                          {r.pvcNamespace} · {formatWhen(r.startedAt)}
                        </p>
                        <p className="mt-1 font-mono text-xs text-muted-foreground">
                          {r.restoreId.slice(0, 8)}
                        </p>
                      </TableCell>
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
                          {r.cancelled ? 'Cancelled' : restoreStepLabel(r.step)}
                          {r.step === 'restoring' && r.progressPercent != null
                            ? ` ${Math.round(r.progressPercent)}%`
                            : ''}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                {items.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={2} className="text-muted-foreground">
                      {loading
                        ? 'Loading restore history…'
                        : error
                          ? 'Restore history unavailable.'
                          : 'No restore jobs yet. Choose a recovery point from Snapshots.'}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <Card className="flex flex-col lg:col-span-3 lg:min-h-0 lg:overflow-hidden">
          <CardHeader className="flex shrink-0 flex-row flex-wrap items-start justify-between gap-2 space-y-0">
            <div>
              <CardTitle>
                {selected
                  ? selected.pvcName || selected.pvcNamespace || 'SQL recovery'
                  : 'Operation details'}
              </CardTitle>
              <CardDescription>
                {selected ? (
                  <>
                    <span className="font-mono text-foreground">
                      {selected.restoreId.slice(0, 8)}
                    </span>
                    {' · '}
                    {restoreStepLabel(selected.step)}
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
                    setCancelTarget(selected)
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
          <CardContent className="flex flex-col lg:min-h-0 lg:flex-1 lg:overflow-hidden">
            {selected && (
              <div className="mb-4 shrink-0">
                <RestoreTimeline restore={selected} />
                <p className="mt-2 break-all text-xs text-muted-foreground">
                  Snapshot: {selected.snapshotId || 'Unknown'}
                  {selected.startedAt && selected.finishedAt
                    ? ' · Duration: ' +
                      Math.max(
                        0,
                        Math.round(
                          (Date.parse(selected.finishedAt) - Date.parse(selected.startedAt)) / 1000,
                        ),
                      ) +
                      's'
                    : ''}
                </p>
              </div>
            )}
            <div className="mb-2 flex shrink-0 flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
              <span>
                Logs · {connected ? 'Live connection' : 'Reconnecting; status refreshes every 8s'}
              </span>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  stickBottom.current = true
                  if (logBoxRef.current)
                    logBoxRef.current.scrollTop = logBoxRef.current.scrollHeight
                }}
              >
                Jump to latest
              </Button>
            </div>
            {logError && (
              <Alert variant="warning" className="mb-2">
                Historical logs unavailable: {logError}
              </Alert>
            )}
            {selected?.lastError && (
              <Alert variant="danger" className="mb-3 shrink-0">
                {selected.lastError}
              </Alert>
            )}
            {selected && selected.step === 'restoring' && selected.progressPercent != null && (
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
              ref={logBoxRef}
              tabIndex={0}
              aria-label="Restore logs"
              className="h-[45dvh] overflow-y-auto overscroll-contain rounded-md border bg-[hsl(var(--log-bg))] p-3 font-mono text-xs leading-relaxed text-[hsl(var(--log-fg))] [overflow-anchor:none] lg:h-auto lg:min-h-24 lg:flex-1"
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
                    ? ['done', 'failed'].includes(selected.step)
                      ? 'No retained log lines for this operation.'
                      : 'Waiting for log lines…'
                    : 'Pick a restore job.'}
                </div>
              ) : (
                lines.map((line, i) => (
                  <div key={i} className="whitespace-pre-wrap break-all">
                    {line}
                  </div>
                ))
              )}
            </div>
            {selected && (
              <div className="mt-3 flex shrink-0 flex-wrap gap-2 text-xs text-muted-foreground">
                  {selected.step === 'done' && selected.argoSyncResumed === true && (
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
      <ConfirmAction
        open={!!cancelTarget}
        onClose={() => setCancelTarget(null)}
        title="Cancel restore?"
        description={`Stop the restore for ${cancelTarget?.pvcNamespace}/${cancelTarget?.pvcName || cancelTarget?.dbPod || 'database'}. The data may be partially restored. Cleanup will attempt to restart workloads and resume Argo CD.`}
        action="Cancel restore"
        onConfirm={async () => {
          if (cancelTarget) {
            await api.cancelRestore(cancelTarget.restoreId)
            await load()
          }
        }}
      />
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
