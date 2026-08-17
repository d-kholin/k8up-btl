import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import type { K8sObject } from '../api'
import { snapTime } from '../lib/snapshots'
import { cn } from '../lib/utils'
import { Button } from './ui/button'

// Month calendar over snapshot history: days with restore points are shaded by
// count; picking a day filters the snapshot list. Built for long histories
// where a flat list stops scaling.

export function dayKey(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

type DayInfo = { count: number; namespaces: Set<string> }

export default function SnapshotCalendar({
  snapshots,
  selected,
  onSelect,
}: {
  snapshots: K8sObject[]
  selected: string | null
  onSelect: (day: string | null) => void
}) {
  // Month being viewed (first of month, local time).
  const [month, setMonth] = useState(() => {
    const now = new Date()
    return new Date(now.getFullYear(), now.getMonth(), 1)
  })
  // Jump to the latest snapshot's month once when data first arrives.
  const jumped = useRef(false)
  useEffect(() => {
    if (jumped.current || snapshots.length === 0) return
    jumped.current = true
    const latest = Math.max(...snapshots.map(snapTime))
    if (latest > 0) {
      const d = new Date(latest)
      setMonth(new Date(d.getFullYear(), d.getMonth(), 1))
    }
  }, [snapshots])

  const byDay = useMemo(() => {
    const m = new Map<string, DayInfo>()
    for (const s of snapshots) {
      const t = snapTime(s)
      if (!t) continue
      const key = dayKey(new Date(t))
      let info = m.get(key)
      if (!info) {
        info = { count: 0, namespaces: new Set() }
        m.set(key, info)
      }
      info.count++
      info.namespaces.add(s.namespace || 'default')
    }
    return m
  }, [snapshots])

  const maxCount = useMemo(() => {
    let max = 0
    for (const info of byDay.values()) max = Math.max(max, info.count)
    return max
  }, [byDay])

  const todayKey = dayKey(new Date())
  const year = month.getFullYear()
  const mon = month.getMonth()
  const firstWeekday = new Date(year, mon, 1).getDay() // 0 = Sunday
  const daysInMonth = new Date(year, mon + 1, 0).getDate()
  const monthLabel = month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })

  const cells: Array<{ key: string; day: number; info?: DayInfo } | null> = []
  for (let i = 0; i < firstWeekday; i++) cells.push(null)
  for (let day = 1; day <= daysInMonth; day++) {
    const key = dayKey(new Date(year, mon, day))
    cells.push({ key, day, info: byDay.get(key) })
  }

  function intensity(count: number): string {
    if (maxCount === 0) return ''
    const ratio = count / maxCount
    if (ratio > 0.75) return 'bg-primary text-primary-foreground'
    if (ratio > 0.5) return 'bg-primary/70 text-primary-foreground'
    if (ratio > 0.25) return 'bg-primary/40'
    return 'bg-primary/20'
  }

  return (
    <div className="w-full max-w-[320px] select-none">
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
              disabled={!c.info}
              onClick={() => onSelect(selected === c.key ? null : c.key)}
              title={
                c.info
                  ? `${c.key} · ${c.info.count} snapshot${c.info.count === 1 ? '' : 's'} · ${[...c.info.namespaces].sort().join(', ')}`
                  : c.key
              }
              className={cn(
                'flex h-8 flex-col items-center justify-center rounded-md text-xs transition-colors',
                c.info ? cn(intensity(c.info.count), 'cursor-pointer hover:ring-1 hover:ring-ring') : 'text-muted-foreground/50',
                selected === c.key && 'ring-2 ring-ring',
                c.key === todayKey && !selected && 'outline outline-1 outline-border',
              )}
            >
              <span className="leading-none">{c.day}</span>
              {c.info && <span className="text-[9px] leading-none opacity-80">{c.info.count}</span>}
            </button>
          ),
        )}
      </div>
      <div className="mt-2 flex items-center justify-between text-[11px] text-muted-foreground">
        <span>shaded = restore points that day</span>
        {selected && (
          <button type="button" className="underline hover:text-foreground" onClick={() => onSelect(null)}>
            clear day filter
          </button>
        )}
      </div>
    </div>
  )
}
