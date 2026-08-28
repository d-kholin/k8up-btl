import { Link } from 'react-router-dom'
import { Loader2, SquareTerminal } from 'lucide-react'
import type { LiveJob } from '../lib/jobs'
import { formatAge } from '../lib/utils'
import { Badge } from './ui/badge'
import { Card, CardContent } from './ui/card'

/** Horizontal strip of currently-running K8up jobs. Renders nothing when idle.
 * With onSelect set, each job gets a console button that opens its live logs. */
export default function ActiveJobs({
  jobs,
  linkNamespace = true,
  onSelect,
}: {
  jobs: LiveJob[]
  linkNamespace?: boolean
  onSelect?: (job: LiveJob) => void
}) {
  const active = jobs.filter((j) => !j.finished)
  if (active.length === 0) return null
  const now = Date.now()
  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-2 py-3">
        <span className="flex items-center gap-1.5 text-sm font-medium">
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          Active jobs
        </span>
        {active.map((j) => (
          <span
            key={`${j.kind}/${j.namespace}/${j.name}`}
            className="flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-xs"
          >
            <Badge variant="secondary" className="capitalize">
              {j.kind}
            </Badge>
            {linkNamespace ? (
              <Link to={`/workloads/${encodeURIComponent(j.namespace)}`} className="font-mono hover:underline">
                {j.namespace}/{j.name}
              </Link>
            ) : (
              <span className="font-mono">{j.name}</span>
            )}
            {j.createdAt && (
              <span className="text-muted-foreground">{formatAge(now - new Date(j.createdAt).getTime())}</span>
            )}
            {onSelect && (
              <button
                type="button"
                onClick={() => onSelect(j)}
                title="Open live console"
                className="text-muted-foreground transition-colors hover:text-foreground"
              >
                <SquareTerminal className="h-3.5 w-3.5" />
              </button>
            )}
          </span>
        ))}
      </CardContent>
    </Card>
  )
}
