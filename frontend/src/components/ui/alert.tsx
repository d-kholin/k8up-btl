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
        variant === 'default' && 'border-border bg-card',
        variant === 'warning' && 'border-amber-500/40 bg-amber-500/10 text-amber-100',
        variant === 'danger' && 'border-red-500/40 bg-red-500/10 text-red-100',
        className,
      )}
      {...props}
    />
  )
}
