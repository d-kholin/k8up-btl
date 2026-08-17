import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatBytes(n?: number | null) {
  if (n == null || Number.isNaN(n)) return '—'
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < u.length - 1)
  return `${v.toFixed(1)} ${u[i]}`
}

export function formatWhen(iso?: string) {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

export function formatAge(ms: number) {
  if (ms < 0) ms = 0
  const m = Math.floor(ms / 60000)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

// cronIntervalMs estimates the expected gap between runs of a cron expression
// (K8up also accepts @daily-random style macros). Conservative: returns the
// largest plausible gap, or null when unparseable.
export function cronIntervalMs(expr?: string): number | null {
  if (!expr) return null
  const HOUR = 3_600_000
  const DAY = 24 * HOUR
  const e = expr.trim().toLowerCase()
  if (e.startsWith('@')) {
    switch (e.replace(/-random$/, '')) {
      case '@yearly':
      case '@annually':
        return 366 * DAY
      case '@monthly':
        return 31 * DAY
      case '@weekly':
        return 7 * DAY
      case '@daily':
      case '@midnight':
        return DAY
      case '@hourly':
        return HOUR
      default:
        return null
    }
  }
  const f = e.split(/\s+/)
  if (f.length < 5) return null
  const [min, hour, dom, , dow] = f
  const step = (s: string) => {
    const m = s.match(/^\*\/(\d+)$/)
    return m ? parseInt(m[1], 10) : null
  }
  if (dom !== '*' && dom !== '?') return 31 * DAY
  if (dow !== '*' && dow !== '?') return 7 * DAY
  const hourStep = step(hour)
  if (hourStep) return hourStep * HOUR
  if (hour === '*') {
    const minStep = step(min)
    return minStep ? minStep * 60_000 : HOUR
  }
  return DAY
}
