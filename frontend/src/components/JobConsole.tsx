import { useEffect, useRef, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { api } from '../api'
import type { LiveJob } from '../lib/jobs'
import { Badge } from './ui/badge'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog'

type ConsoleLine = { kind: 'log' | 'note'; text: string }
type ConsoleEvent = { type?: 'log' | 'note' | 'done'; line?: string; status?: string }

const MAX_LINES = 2000

/** Live console for one K8up job CR: streams the job pod's logs over SSE, with
 * meta notes while no pod exists (admission-rejected / pending jobs) and a
 * best-effort progress bar scraped from restic's percent output. */
export default function JobConsole({
  job,
  open,
  onOpenChange,
}: {
  job: LiveJob | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [lines, setLines] = useState<ConsoleLine[]>([])
  const [done, setDone] = useState<string | null>(null)
  const [percent, setPercent] = useState<number | null>(null)
  const stickBottom = useRef(true)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  // Monotonic like the restore orchestrator: restic interleaves per-file and
  // overall percentages, so only ever move the bar forward.
  const maxPercent = useRef(0)

  useEffect(() => {
    if (!open || !job) return
    setLines([])
    setDone(null)
    setPercent(null)
    maxPercent.current = 0
    stickBottom.current = true

    const es = new EventSource(api.jobConsoleUrl(job.kind, job.namespace, job.name))
    es.onmessage = (ev) => {
      let msg: ConsoleEvent
      try {
        msg = JSON.parse(ev.data) as ConsoleEvent
      } catch {
        return
      }
      if (msg.type === 'done') {
        setDone(msg.status || 'done')
        es.close()
        return
      }
      if ((msg.type === 'log' || msg.type === 'note') && msg.line != null) {
        if (msg.type === 'log') {
          const matches = msg.line.match(/(\d{1,3}(?:\.\d+)?)\s*%/g)
          if (matches) {
            const pct = parseFloat(matches[matches.length - 1])
            if (Number.isFinite(pct) && pct <= 100 && pct > maxPercent.current) {
              maxPercent.current = pct
              setPercent(pct)
            }
          }
        }
        const line: ConsoleLine = { kind: msg.type, text: msg.line }
        setLines((prev) => (prev.length >= MAX_LINES ? [...prev.slice(-MAX_LINES + 1), line] : [...prev, line]))
      }
    }
    es.onerror = () => {
      // EventSource auto-reconnects; the server replays a fresh tail on attach.
    }
    return () => es.close()
  }, [open, job?.kind, job?.namespace, job?.name])

  useEffect(() => {
    if (stickBottom.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [lines])

  const running = !done && !(job?.finished ?? false)
  const failed = done === 'failed' || (job?.failed ?? false)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary" className="capitalize">
              {job?.kind}
            </Badge>
            <span className="font-mono text-sm font-normal">
              {job ? `${job.namespace}/${job.name}` : ''}
            </span>
            {running ? (
              <Badge variant="secondary" className="flex items-center gap-1">
                <Loader2 className="h-3 w-3 animate-spin" /> running
              </Badge>
            ) : failed ? (
              <Badge variant="danger">failed</Badge>
            ) : done === 'gone' ? (
              <Badge variant="warning">gone</Badge>
            ) : (
              <Badge variant="success">succeeded</Badge>
            )}
          </DialogTitle>
          <DialogDescription>
            Live console — job pod logs, plus job status while no pod exists
          </DialogDescription>
        </DialogHeader>
        {percent != null && running && (
          <div>
            <div className="mb-1 flex justify-between text-xs text-muted-foreground">
              <span>restic progress (from job logs)</span>
              <span>{Math.round(percent)}%</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-all"
                style={{ width: `${Math.min(100, percent)}%` }}
              />
            </div>
          </div>
        )}
        <div
          ref={scrollRef}
          className="h-[50dvh] overflow-y-auto overscroll-contain rounded-md border bg-[hsl(var(--log-bg))] p-3 font-mono text-[11px] leading-relaxed text-[hsl(var(--log-fg))] [overflow-anchor:none]"
          onWheel={(e) => e.stopPropagation()}
          onScroll={(e) => {
            const el = e.currentTarget
            stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48
          }}
        >
          {lines.length === 0 ? (
            <div className="text-muted-foreground">Attaching to job…</div>
          ) : (
            lines.map((l, i) => (
              <div
                key={i}
                className={`whitespace-pre-wrap break-all${l.kind === 'note' ? ' italic text-muted-foreground' : ''}`}
              >
                {l.text}
              </div>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
