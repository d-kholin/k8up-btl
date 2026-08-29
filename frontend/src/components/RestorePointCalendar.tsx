import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { dayKey } from './SnapshotCalendar'
import { cn } from '../lib/utils'
import { Button } from './ui/button'

// Restore-point picker for the lab start dialog: a month calendar of the
// namespace's snapshot times (shaded by count), with a time list for days
// that hold more than one point. '' means "latest".

export default function RestorePointCalendar({
  points,
  value,
  onChange,
}: {
  /** ISO timestamps of available restore points (any order). */
  points: string[]
  /** Selected point ISO string, or '' for latest. */
  value: string
  onChange: (v: string) => void
}) {
  const [month, setMonth] = useState(() => {
    const now = new Date()
    return new Date(now.getFullYear(), now.getMonth(), 1)
  })
  // Day whose times are listed; follows the selected value, else user clicks.
  const [openDay, setOpenDay] = useState<string | null>(null)

  // Jump to the newest point's month once when data first arrives.
  const jumped = useRef(false)
  useEffect(() => {
    if (jumped.current || points.length === 0) return
    jumped.current = true
    const latest = Math.max(...points.map((p) => new Date(p).getTime()))
    if (latest > 0) {
      const d = new Date(latest)
      setMonth(new Date(d.getFullYear(), d.getMonth(), 1))
    }
  }, [points])

  const byDay = useMemo(() => {
    const m = new Map<string, string[]>()
    for (const p of points) {
      const t = new Date(p)
      if (isNaN(t.getTime())) continue
      const key = dayKey(t)
      const list = m.get(key) || []
      list.push(p)
      m.set(key, list)
    }
    for (const list of m.values()) list.sort((a, b) => new Date(b).getTime() - new Date(a).getTime())
    return m
  }, [points])

  const valueDay = value ? dayKey(new Date(value)) : null
  const activeDay = openDay || valueDay
  const dayTimes = activeDay ? byDay.get(activeDay) || [] : []

  const year = month.getFullYear()
  const mon = month.getMonth()
  const firstWeekday = new Date(year, mon, 1).getDay()
  const daysInMonth = new Date(year, mon + 1, 0).getDate()
  const monthLabel = month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })

  const cells: Array<{ key: string; day: number; times?: string[] } | null> = []
  for (let i = 0; i < firstWeekday; i++) cells.push(null)
  for (let day = 1; day <= daysInMonth; day++) {
    const key = dayKey(new Date(year, mon, day))
    cells.push({ key, day, times: byDay.get(key) })
  }

  const pickDay = (key: string, times: string[]) => {
    setOpenDay(key)
    // A single point that day needs no second click.
    if (times.length === 1) onChange(times[0])
  }

  return (
    <div className="w-full select-none rounded-md border p-3">
      <div className="mb-2 flex items-center justify-between">
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          onClick={() => setMonth(new Date(year, mon - 1, 1))}
          title="Previous month"
        >
          <ChevronLeft className="h-4 w-4" />
        </Button>
        <div className="text-sm font-medium">{monthLabel}</div>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          onClick={() => setMonth(new Date(year, mon + 1, 1))}
          title="Next month"
        >
          <ChevronRight className="h-4 w-4" />
        </Button>
      </div>
      <div className="grid grid-cols-7 gap-1 text-center">
        {['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'].map((d) => (
          <div key={d} className="py-1 text-[10px] font-medium text-muted-foreground">
            {d}
          </div>
        ))}
        {cells.map((c, i) =>
          c === null ? (
            <div key={`blank-${i}`} />
          ) : (
            <button
              key={c.key}
              type="button"
              disabled={!c.times}
              onClick={() => pickDay(c.key, c.times!)}
              title={c.times ? `${c.key} · ${c.times.length} restore point${c.times.length === 1 ? '' : 's'}` : c.key}
              className={cn(
                'flex h-8 flex-col items-center justify-center rounded-md text-xs transition-colors',
                c.times ? 'cursor-pointer bg-primary/20 hover:ring-1 hover:ring-ring' : 'text-muted-foreground/50',
                valueDay === c.key && 'bg-primary text-primary-foreground',
                activeDay === c.key && 'ring-2 ring-ring',
              )}
            >
              <span className="leading-none">{c.day}</span>
              {c.times && <span className="text-[9px] leading-none opacity-80">{c.times.length}</span>}
            </button>
          ),
        )}
      </div>
      {activeDay && dayTimes.length > 1 && (
        <div className="mt-2">
          <div className="mb-1 text-[11px] text-muted-foreground">
            {dayTimes.length} restore points on {activeDay} — pick one:
          </div>
          <div className="flex flex-wrap gap-1">
            {dayTimes.map((p) => (
              <button
                key={p}
                type="button"
                onClick={() => onChange(p)}
                className={cn(
                  'rounded-md border px-2 py-1 font-mono text-[11px] transition-colors hover:bg-row-hover',
                  value === p && 'border-primary bg-primary/15 font-medium',
                )}
              >
                {new Date(p).toLocaleTimeString()}
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="mt-2 flex items-center justify-between text-[11px] text-muted-foreground">
        <span>
          {value ? `restore point: ${new Date(value).toLocaleString()}` : 'no day picked — using latest snapshots'}
        </span>
        {value && (
          <button
            type="button"
            className="underline hover:text-foreground"
            onClick={() => {
              onChange('')
              setOpenDay(null)
            }}
          >
            use latest
          </button>
        )}
      </div>
    </div>
  )
}
