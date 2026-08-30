import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, Copy, FlaskConical, ShieldCheck, Timer, X } from 'lucide-react'
import { api, type DrillStatus, type LabOverview, type LabPlan, type LabState } from '../api'
import { cn, formatAge, formatWhen } from '../lib/utils'
import RestorePointCalendar from '../components/RestorePointCalendar'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

const MAX_LINES = 1500

function stepBadge(step: string) {
  if (step === 'ready') return <Badge variant="success">ready to test</Badge>
  if (step === 'deleted') return <Badge variant="secondary">torn down</Badge>
  if (step === 'failed') return <Badge variant="danger">failed</Badge>
  return <Badge variant="warning">{step.replaceAll('_', ' ')}</Badge>
}

// Local half of a suggested port-forward: binding a privileged port (<1024)
// needs root, so 80→8080, 443→8443 etc.; unprivileged ports map to themselves.
function localPort(p: number) {
  return p < 1024 ? p + 8000 : p
}

function verdictBadge(lab: LabState) {
  if (!lab.verdict) return null
  return (
    <Badge variant={lab.verdict === 'passed' ? 'success' : 'danger'}>test {lab.verdict}</Badge>
  )
}

/** Only an operator's pass verdict verifies a restore; "restored" (and legacy
 * "success") means the data came back but nobody judged it yet. */
function drillBadge(status: string) {
  if (status === 'passed') return <Badge variant="success">verified</Badge>
  if (status === 'restored' || status === 'success')
    return <Badge variant="warning">restored — not tested</Badge>
  return <Badge variant="danger">{status}</Badge>
}

function CopyBox({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex max-w-full items-center gap-1 rounded-md border bg-background px-2 py-1">
      <span className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs">{text}</span>
      <button
        type="button"
        title="Copy to clipboard"
        className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
        onClick={() => {
          navigator.clipboard
            .writeText(text)
            .then(() => {
              setCopied(true)
              window.setTimeout(() => setCopied(false), 1500)
            })
            .catch(() => {})
        }}
      >
        {copied ? <Check className="h-3.5 w-3.5 text-green-600" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
    </div>
  )
}

export default function Lab() {
  const [overview, setOverview] = useState<LabOverview | null>(null)
  const [error, setError] = useState('')
  const [drills, setDrills] = useState<DrillStatus[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [logs, setLogs] = useState<Record<string, string[]>>({})
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [, setTick] = useState(0) // re-render for TTL countdown
  const logBoxRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)

  const load = () =>
    api
      .lab()
      .then((o) => {
        setOverview(o)
        // Follow the active lab's log; with no lab running nothing opens by
        // itself — the operator picks a past run from History if they want one.
        setSelectedId((cur) => {
          if (cur && o.labs.some((l) => l.labId === cur)) return cur
          return o.current?.labId || null
        })
      })
      .catch((e: Error) => setError(e.message))

  const loadDrills = () => api.labVerified().then(setDrills).catch(() => {})

  useEffect(() => {
    load()
    loadDrills()
    const es = new EventSource('/api/v1/events')
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as { type?: string; labId?: string; line?: string }
        if (msg.type === 'lab') {
          load()
          loadDrills()
          return
        }
        if (msg.type === 'lab-log' && msg.labId && msg.line != null) {
          setLogs((prev) => {
            const prevLines = prev[msg.labId!] || []
            const next = [...prevLines, msg.line!]
            return { ...prev, [msg.labId!]: next.length > MAX_LINES ? next.slice(-MAX_LINES) : next }
          })
        }
      } catch {
        load()
      }
    }
    const t = setInterval(load, 10000)
    const tick = setInterval(() => setTick((n) => n + 1), 30000)
    return () => {
      es.close()
      clearInterval(t)
      clearInterval(tick)
    }
  }, [])

  // Catch-up log buffer when selection changes.
  useEffect(() => {
    if (!selectedId) return
    api
      .labLogs(selectedId)
      .then((res) => {
        setLogs((prev) => {
          const live = prev[selectedId] || []
          const merged = res.lines.length >= live.length ? res.lines : [...res.lines, ...live.slice(res.lines.length)]
          return { ...prev, [selectedId]: merged.slice(-MAX_LINES) }
        })
      })
      .catch(() => {})
  }, [selectedId])

  // Re-pin to the tail when switching labs, then keep the LOG BOX (never the
  // page) pinned while new lines stream in — scrollIntoView would drag every
  // scrollable ancestor down with it.
  useEffect(() => {
    stickBottom.current = true
  }, [selectedId])

  useEffect(() => {
    const el = logBoxRef.current
    if (stickBottom.current && el) el.scrollTop = el.scrollHeight
  }, [logs, selectedId])

  const current = overview?.current || null
  const selected = useMemo(
    () => overview?.labs.find((l) => l.labId === selectedId) || null,
    [overview, selectedId],
  )
  const lines = selectedId ? logs[selectedId] || [] : []

  const teardown = (lab: LabState) => {
    const provisioning = !['ready', 'failed'].includes(lab.step)
    if (
      !window.confirm(
        `${provisioning ? 'Cancel provisioning and tear' : 'Tear'} down the lab for ${lab.sourceNamespace}? This deletes the clone app, every PVC in ${lab.labNamespace}, and their volumes.`,
      )
    )
      return
    api.labTeardown(lab.labId).then(load).catch((e: Error) => setError(e.message))
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <FlaskConical className="h-6 w-6" /> Restore Lab
          </h1>
          <p className="text-sm text-muted-foreground">
            Restore snapshots into an isolated namespace — inspect the data or test the running app —
            and prove your backups restore.
          </p>
        </div>
        <Button
          disabled={!overview?.enabled || !!current}
          title={current ? 'One lab at a time — tear the active lab down first' : undefined}
          onClick={() => setDialogOpen(true)}
        >
          Start lab
        </Button>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}
      {overview && !overview.enabled && (
        <Alert variant="warning">
          The Restore Lab is not enabled. Deploy <span className="font-mono text-xs">deploy/k8s/restore-lab</span> and
          set <span className="font-mono text-xs">RESTORE_LAB_NAMESPACE=restore-lab</span> on the backend — see
          docs/restore-lab.md.
        </Alert>
      )}

      {current && (
        <Card>
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-2 space-y-0">
            <div>
              <CardTitle className="flex items-center gap-2">
                {current.sourceNamespace}
                {stepBadge(current.step)}
                <Badge variant="secondary">{current.tier} tier</Badge>
              </CardTitle>
              <CardDescription>
                {current.tier === 'app'
                  ? `Clone of ${current.appName || current.sourceNamespace} running in ${current.labNamespace}`
                  : `Snapshot data mounted read-only in ${current.labNamespace}`}
                {' · started '}
                {formatWhen(current.startedAt)}
                {current.actor ? ` by ${current.actor}` : ''}
              </CardDescription>
            </div>
            <div className="flex gap-2">
              {current.step === 'ready' && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    api.labExtend(current.labId, 24).then(load).catch((e: Error) => setError(e.message))
                  }
                >
                  <Timer className="mr-1 h-4 w-4" /> +24h
                </Button>
              )}
              {!['deleted', 'tearing_down'].includes(current.step) && (
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={current.cancelRequested}
                  onClick={() => teardown(current)}
                >
                  {['ready', 'failed'].includes(current.step)
                    ? 'Tear down'
                    : current.cancelRequested
                      ? 'Cancelling…'
                      : 'Cancel & tear down'}
                </Button>
              )}
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {current.lastError && <Alert variant="danger">{current.lastError}</Alert>}
            <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm">
              {current.step === 'ready' && current.expiresAt && (
                <span className="text-muted-foreground">
                  auto-teardown in{' '}
                  <span className="font-medium text-foreground">
                    {formatAge(Math.max(0, new Date(current.expiresAt).getTime() - Date.now()))}
                  </span>
                </span>
              )}
              {current.healthyAt && (
                <span className="text-muted-foreground">
                  healthy in{' '}
                  <span className="font-medium text-foreground">
                    {formatAge(new Date(current.healthyAt).getTime() - new Date(current.startedAt).getTime())}
                  </span>{' '}
                  (RTO actual)
                </span>
              )}
              {current.health && <span className="text-muted-foreground">app health: {current.health}</span>}
              {current.restorePoint && (
                <span className="text-muted-foreground">
                  restore point: <span className="font-medium text-foreground">{formatWhen(current.restorePoint)}</span>
                </span>
              )}
            </div>
            {!!current.pvcs?.length && (
              <div className="flex flex-wrap gap-2 text-xs">
                {current.pvcs.map((p) => (
                  <Badge
                    key={p.pvcName}
                    variant={p.status === 'done' ? 'success' : p.status === 'failed' ? 'danger' : 'secondary'}
                  >
                    {p.pvcName} {p.status === 'done' ? '✓' : p.status || ''}
                  </Badge>
                ))}
                {current.dumpSnapshot && <Badge variant="secondary">SQL dump: {current.dumpPath}</Badge>}
              </div>
            )}
            {current.step === 'ready' && (
              <div className="space-y-1 rounded-md border bg-muted/30 p-3">
                <div className="text-sm font-medium">Connect</div>
                {current.services?.length ? (
                  <div className="space-y-1">
                    {current.services.map((s) =>
                      (s.ports?.length ? s.ports : [80]).map((p) => (
                        <div key={`${s.name}:${p}`} className="space-y-1">
                          <div className="font-mono text-xs">
                            {s.name}.{current.labNamespace}.svc.cluster.local:{p}
                          </div>
                          <CopyBox
                            text={`kubectl -n ${current.labNamespace} port-forward svc/${s.name} ${localPort(p)}:${p}`}
                          />
                        </div>
                      )),
                    )}
                    <p className="pt-1 text-xs text-muted-foreground">
                      Point the tunnel (Pangolin resource) at the service target, or use the port-forward.
                    </p>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    No Services found in <span className="font-mono">{current.labNamespace}</span> — check the lab log.
                  </p>
                )}
              </div>
            )}
            {current.verdict ? (
              <div className="flex flex-wrap items-baseline gap-2 text-sm">
                {verdictBadge(current)}
                <span className="text-xs text-muted-foreground">
                  by {current.verdictBy} {current.verdictAt ? `· ${formatWhen(current.verdictAt)}` : ''}
                </span>
                {current.verdictNote && <span className="text-xs">{current.verdictNote}</span>}
              </div>
            ) : (
              current.readyAt &&
              current.step !== 'deleted' && (
                <VerdictForm
                  lab={current}
                  onDone={load}
                  onError={setError}
                />
              )
            )}
          </CardContent>
        </Card>
      )}

      {selectedId && (
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-2 space-y-0">
          <div>
            <CardTitle>Lab log</CardTitle>
            <CardDescription>
              {selected ? (
                <>
                  <span className="font-mono text-foreground">{selected.labId.slice(0, 8)}</span>
                  {' · '}
                  {selected.sourceNamespace}
                  {' · '}
                  {selected.step}
                </>
              ) : (
                'No lab selected'
              )}
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {overview && overview.labs.length > 1 && (
              <select
                value={selectedId || ''}
                onChange={(e) => setSelectedId(e.target.value)}
                className="h-8 rounded-md border border-input bg-background px-2 text-xs"
              >
                {overview.labs.map((l) => (
                  <option key={l.labId} value={l.labId}>
                    {l.sourceNamespace} · {formatWhen(l.startedAt)} · {l.step}
                  </option>
                ))}
              </select>
            )}
            {(!current || selectedId !== current.labId) && (
              <button
                type="button"
                title="Close log"
                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                onClick={() => setSelectedId(current?.labId || null)}
              >
                <X className="h-4 w-4" />
              </button>
            )}
          </div>
        </CardHeader>
        <CardContent>
          <div
            ref={logBoxRef}
            className="h-[40dvh] overflow-y-auto rounded-md border bg-[hsl(var(--log-bg))] p-3 font-mono text-[11px] leading-relaxed text-[hsl(var(--log-fg))] [overflow-anchor:none]"
            onScroll={(e) => {
              const el = e.currentTarget
              stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
            }}
          >
            {lines.length === 0 ? (
              <div className="text-muted-foreground">
                {selected ? 'No log lines yet.' : 'Start a lab to see its log here.'}
              </div>
            ) : (
              lines.map((line, i) => (
                <div key={i} className="whitespace-pre-wrap break-all">
                  {line}
                </div>
              ))
            )}
          </div>
        </CardContent>
      </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4" /> Restore verification
          </CardTitle>
          <CardDescription>
            Latest drill per namespace — verified means an operator tested the restore and marked it passed.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {drills.length === 0 ? (
            <p className="text-sm text-muted-foreground">No drills recorded yet. Start a lab to create evidence.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Namespace</TableHead>
                  <TableHead>Last drill</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead className="hidden md:table-cell">Note</TableHead>
                  <TableHead className="hidden sm:table-cell">Last verified</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {drills.map((d) => (
                  <TableRow key={d.namespace}>
                    <TableCell className="font-mono text-xs">{d.namespace}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {formatWhen(d.lastAt)}
                    </TableCell>
                    <TableCell>{drillBadge(d.lastStatus)}</TableCell>
                    <TableCell className="hidden max-w-[20rem] md:table-cell">
                      <span className="block truncate text-xs text-muted-foreground" title={d.lastNote}>
                        {d.lastNote || '—'}
                      </span>
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground sm:table-cell">
                      {d.lastPassedAt ? formatAge(Date.now() - new Date(d.lastPassedAt).getTime()) + ' ago' : 'never'}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>History</CardTitle>
          <CardDescription>Recent lab runs — select one to view its log</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Source</TableHead>
                <TableHead>Tier</TableHead>
                <TableHead>Step</TableHead>
                <TableHead>Result</TableHead>
                <TableHead className="hidden sm:table-cell">Started</TableHead>
                <TableHead className="hidden md:table-cell">Torn down by</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(overview?.labs || []).map((l) => (
                <TableRow
                  key={l.labId}
                  className={cn('cursor-pointer', selectedId === l.labId && 'bg-muted/60')}
                  onClick={() => setSelectedId(l.labId)}
                >
                  <TableCell className="font-mono text-xs">{l.sourceNamespace}</TableCell>
                  <TableCell className="text-xs">{l.tier}</TableCell>
                  <TableCell>{stepBadge(l.step)}</TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-0.5">
                      {verdictBadge(l) || <span className="text-xs text-muted-foreground">—</span>}
                      {l.verdictNote && (
                        <span className="max-w-[16rem] truncate text-xs text-muted-foreground" title={l.verdictNote}>
                          {l.verdictNote}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground sm:table-cell">
                    {formatWhen(l.startedAt)}
                  </TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                    {l.tornDownBy || '—'}
                  </TableCell>
                </TableRow>
              ))}
              {(overview?.labs || []).length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    No lab runs yet.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <StartLabDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onStarted={(st) => {
          setDialogOpen(false)
          // Focus the NEW lab's log, not whatever was selected before.
          setSelectedId(st.labId)
          load()
        }}
        onError={setError}
      />
    </div>
  )
}

/** Operator judgement on a ready lab: pass/fail + note — recorded as the
 * namespace's latest drill evidence. */
function VerdictForm({
  lab,
  onDone,
  onError,
}: {
  lab: LabState
  onDone: () => void
  onError: (msg: string) => void
}) {
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = (status: 'passed' | 'failed') => {
    setBusy(true)
    api
      .labVerdict(lab.labId, status, note.trim())
      .then(() => onDone())
      .catch((e: Error) => onError(e.message))
      .finally(() => setBusy(false))
  }

  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="text-sm font-medium">Record test result</div>
      <input
        type="text"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Note (what was checked, what you found)…"
        maxLength={2000}
        className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      />
      <div className="flex gap-2">
        <Button size="sm" disabled={busy} onClick={() => submit('passed')}>
          Mark passed
        </Button>
        <Button size="sm" variant="destructive" disabled={busy} onClick={() => submit('failed')}>
          Mark failed
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Becomes the latest drill evidence for {lab.sourceNamespace} — shown on the dashboard's restore
        verification panel.
      </p>
    </div>
  )
}

/** Namespace → plan preview → tier choice → go. The plan shows exactly what
 * will be restored (newest snapshot per PVC + SQL dump) before anything runs. */
function StartLabDialog({
  open,
  onOpenChange,
  onStarted,
  onError,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onStarted: (st: LabState) => void
  onError: (msg: string) => void
}) {
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [ns, setNs] = useState('')
  const [tier, setTier] = useState<'data' | 'app'>('app')
  const [plan, setPlan] = useState<LabPlan | null>(null)
  const [planError, setPlanError] = useState('')
  const [dataPVC, setDataPVC] = useState('')
  // point is the chosen restore-point cutoff ('' = latest); the plan is
  // recomputed server-side for it, so the preview always shows the exact
  // snapshots that will restore.
  const [point, setPoint] = useState('')
  const [points, setPoints] = useState<string[]>([])
  const [ttlHours, setTtlHours] = useState(24)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setError('')
    setBusy(false)
    api
      .snapshots()
      .then((snaps) => {
        const set = new Set<string>()
        for (const s of snaps) if (s.namespace) set.add(s.namespace)
        const list = [...set].sort()
        setNamespaces(list)
        setNs((cur) => cur || list[0] || '')
      })
      .catch((e: Error) => setError(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // New namespace → forget the previous namespace's restore point.
  useEffect(() => {
    setPoint('')
    setPoints([])
  }, [ns])

  useEffect(() => {
    if (!open || !ns) return
    setPlan(null)
    setPlanError('')
    setDataPVC('')
    api
      .labPlan(ns, point || undefined)
      .then((p) => {
        setPlan(p)
        setPoints(p.restorePoints || [])
        setDataPVC(p.pvcs.find((x) => x.sourceExists)?.pvcName || '')
        setTier((t) => (p.app ? t : 'data'))
      })
      .catch((e: Error) => setPlanError(e.message))
  }, [open, ns, point])

  const appAvailable = !!plan?.app
  const canStart =
    !!ns && !!plan && !busy && (tier === 'app' ? appAvailable : !!dataPVC)

  const start = () => {
    if (!plan) return
    setBusy(true)
    setError('')
    const body =
      tier === 'app'
        ? { sourceNamespace: ns, tier: 'app' as const, ttlHours, before: point || undefined }
        : {
            // Data tier: the snapshot comes from the point-filtered plan, so
            // the chosen restore point is already baked in.
            sourceNamespace: ns,
            tier: 'data' as const,
            ttlHours,
            pvcName: dataPVC,
            snapshotName: plan.pvcs.find((p) => p.pvcName === dataPVC)?.snapshotName,
          }
    api
      .labStart(body)
      .then((st) => onStarted(st))
      .catch((e: Error) => {
        setError(e.message)
        onError('')
        setBusy(false)
      })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Start a restore lab</DialogTitle>
          <DialogDescription>
            Restores land in the isolated lab namespace. Nothing in the source namespace is touched — no Argo pause,
            no scale-down.
          </DialogDescription>
        </DialogHeader>
        {error && <Alert variant="danger">{error}</Alert>}
        <div className="space-y-2">
          <label htmlFor="lab-ns" className="text-sm font-medium">
            Source namespace
          </label>
          <select
            id="lab-ns"
            value={ns}
            onChange={(e) => setNs(e.target.value)}
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {namespaces.length === 0 && <option value="">No namespaces with snapshots</option>}
            {namespaces.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
        {points.length > 0 && (
          <div className="space-y-2">
            <span className="text-sm font-medium">Restore point</span>
            <RestorePointCalendar points={points} value={point} onChange={setPoint} />
            <p className="text-xs text-muted-foreground">
              Each PVC (and the SQL dump) restores from its newest snapshot at or before this point.
            </p>
          </div>
        )}
        {planError && <Alert variant="danger">{planError}</Alert>}
        {plan && (
          <>
            <div className="space-y-2">
              <span className="text-sm font-medium">Tier</span>
              <div className="flex flex-col gap-2">
                <label className={cn('flex cursor-pointer items-start gap-2 rounded-md border p-2 text-sm', tier === 'app' && 'border-primary')}>
                  <input
                    type="radio"
                    name="lab-tier"
                    checked={tier === 'app'}
                    disabled={!appAvailable}
                    onChange={() => setTier('app')}
                    className="mt-1"
                  />
                  <span>
                    <span className="font-medium">App lab</span>
                    <span className="block text-xs text-muted-foreground">
                      {appAvailable ? (
                        <>
                          Deploy <span className="font-mono">{plan.app!.name}</span> from{' '}
                          <span className="font-mono">{plan.app!.labPath || plan.app!.path}</span> against the restored
                          data{plan.dump ? ' and replay the SQL dump' : ''}.
                        </>
                      ) : (
                        plan.appError || 'Unavailable for this namespace.'
                      )}
                    </span>
                  </span>
                </label>
                <label className={cn('flex cursor-pointer items-start gap-2 rounded-md border p-2 text-sm', tier === 'data' && 'border-primary')}>
                  <input
                    type="radio"
                    name="lab-tier"
                    checked={tier === 'data'}
                    onChange={() => setTier('data')}
                    className="mt-1"
                  />
                  <span>
                    <span className="font-medium">Data lab</span>
                    <span className="block text-xs text-muted-foreground">
                      Restore one PVC snapshot and browse the files read-only.
                    </span>
                  </span>
                </label>
              </div>
            </div>
            {tier === 'app' && (
              <div className="space-y-1 text-xs text-muted-foreground">
                <span className="text-sm font-medium text-foreground">Will restore</span>
                {plan.pvcs
                  .filter((p) => p.sourceExists)
                  .map((p) => (
                    <div key={p.pvcName} className="font-mono">
                      {p.pvcName} ← {p.snapshotName} ({formatWhen(p.date)})
                    </div>
                  ))}
                {plan.dump && (
                  <div className="font-mono">
                    {plan.dump.path} ← {plan.dump.snapshotName} ({formatWhen(plan.dump.date)})
                  </div>
                )}
                {plan.warnings?.map((w, i) => (
                  <Alert key={i} variant="warning">
                    {w}
                  </Alert>
                ))}
              </div>
            )}
            {tier === 'data' && (
              <div className="space-y-2">
                <label htmlFor="lab-pvc" className="text-sm font-medium">
                  PVC (newest snapshot)
                </label>
                <select
                  id="lab-pvc"
                  value={dataPVC}
                  onChange={(e) => setDataPVC(e.target.value)}
                  className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {plan.pvcs.filter((p) => p.sourceExists).length === 0 && (
                    <option value="">No PVC snapshots in this namespace</option>
                  )}
                  {plan.pvcs
                    .filter((p) => p.sourceExists)
                    .map((p) => (
                      <option key={p.pvcName} value={p.pvcName}>
                        {p.pvcName} — {formatWhen(p.date)}
                      </option>
                    ))}
                </select>
              </div>
            )}
            <div className="space-y-2">
              <label htmlFor="lab-ttl" className="text-sm font-medium">
                Auto-teardown after (hours)
              </label>
              <input
                id="lab-ttl"
                type="number"
                min={1}
                max={168}
                value={ttlHours}
                onChange={(e) => setTtlHours(Math.max(1, Math.min(168, Number(e.target.value) || 24)))}
                className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
          </>
        )}
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!canStart} onClick={start}>
            {busy ? 'Starting…' : 'Start lab'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
