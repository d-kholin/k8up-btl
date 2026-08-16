import * as React from "react"
import { cn } from '@/lib/utils'

export function Alert({
  className,
  variant = 'default',
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { variant?: 'default' | 'warning' | 'danger' }) {
  return (
    <div
      role="alert"
      className={cn(
        'relative w-full rounded-lg border px-4 py-3 text-sm',
        variant === 'default' && 'border-border bg-card text-foreground',
        // Light mode needs dark text; pale amber/red-100 was unreadable on light backgrounds.
        variant === 'warning' &&
          'border-amber-600/50 bg-amber-50 text-amber-950 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-100',
        variant === 'danger' &&
          'border-red-600/50 bg-red-50 text-red-950 dark:border-red-500/40 dark:bg-red-500/10 dark:text-red-100',
        className,
      )}
      {...props}
    />
  )
}
