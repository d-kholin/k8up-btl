import type { RestoreState } from '../api'
import { cn } from '../lib/utils'
import { Badge } from './ui/badge'

export function restoreStepLabel(step: string) {
  return (
    (
      {
        queued: 'Preparing',
        pausing_argo: 'Pausing Argo CD',
        scaling_down: 'Stopping workload',
        quiescing: 'Stopping workloads',
        safety_backup: 'Safety backup',
        restoring: 'Restoring volume',
        restoring_db: 'Restoring database',
        restoring_pvcs: 'Restoring volumes',
        scaling_up: 'Starting workloads',
        resuming_argo: 'Resuming Argo CD',
        done: 'Completed',
        failed: 'Failed',
      } as Record<string, string>
    )[step] || step.replaceAll('_', ' ')
  )
}

export default function RestoreTimeline({ restore }: { restore: RestoreState }) {
  const stages = [
    ['Prepare', ['queued']],
    ['Pause Argo CD', ['pausing_argo']],
    ['Stop workloads', ['scaling_down', 'quiescing']],
    ['Restore data', ['safety_backup', 'restoring', 'restoring_db', 'restoring_pvcs']],
    ['Resume services', ['scaling_up', 'resuming_argo']],
    ['Complete', ['done']],
  ] as const
  const current = stages.findIndex(([, steps]) =>
    (steps as readonly string[]).includes(restore.step),
  )
  // Failed states do not retain a last successful stage. Never invent completion history.
  return (
    <section
      aria-label="Restore progress"
      className="space-y-4 rounded-lg border bg-card p-4 sm:p-5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-semibold">
          {restore.cancelled ? 'Restore cancelled' : restoreStepLabel(restore.step)}
        </h2>
        <Badge
          variant={
            restore.argoSyncResumed === true
              ? 'success'
              : restore.argoSyncResumed === false
                ? 'danger'
                : 'secondary'
          }
        >
          {restore.argoSyncResumed === true
            ? 'Argo CD resumed'
            : restore.argoSyncResumed === false
              ? 'Argo CD not resumed'
              : restore.argoPausedGlobally
                ? 'Argo CD globally paused'
                : 'Argo CD status not confirmed'}
        </Badge>
      </div>
      {current >= 0 ? (
        <ol className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
          {stages.map(([label], index) => (
            <li
              key={label}
              aria-current={index === current ? 'step' : undefined}
              className={cn(
                'border-t-2 pt-2 text-xs',
                index <= current
                  ? 'border-primary text-foreground'
                  : 'border-border text-muted-foreground',
              )}
            >
              <span className="mb-1 block text-xs text-muted-foreground">
                {index < current ? '✓' : index + 1}
              </span>
              {label}
            </li>
          ))}
        </ol>
      ) : (
        <p className="text-sm text-muted-foreground">
          The operation stopped. Review the error and cleanup status before taking another action.
        </p>
      )}
      {restore.lastError && (
        <p className="break-words text-sm text-destructive">{restore.lastError}</p>
      )}
    </section>
  )
}
