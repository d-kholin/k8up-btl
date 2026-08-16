import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-primary/15 text-primary',
        secondary: 'border-transparent bg-secondary text-secondary-foreground',
        outline: 'text-foreground',
        success:
          'border-transparent bg-emerald-600/15 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-400',
        warning:
          'border-transparent bg-amber-600/15 text-amber-900 dark:bg-amber-500/15 dark:text-amber-400',
        danger:
          'border-transparent bg-red-600/15 text-red-800 dark:bg-red-500/15 dark:text-red-400',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export function Badge({
  className,
  variant,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & VariantProps<typeof badgeVariants>) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}
