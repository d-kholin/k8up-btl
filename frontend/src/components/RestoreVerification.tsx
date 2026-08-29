import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { FlaskConical } from 'lucide-react'
import { api, type DrillStatus, type K8sObject } from '../api'
import { formatAge, formatWhen } from '../lib/utils'
import { Badge } from './ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

type Row = {
  namespace: string
  state: 'verified' | 'failed' | 'never'
  lastAt?: string
  lastSuccessAt?: string
}

/** "Last verified restore per app" — the evidence panel restore drills exist
 * for: every backed-up namespace vs its most recent Restore Lab drill. */
export default function RestoreVerification({ schedules }: { schedules: K8sObject[] }) {
  const [drills, setDrills] = useState<DrillStatus[] | null>(null)

  useEffect(() => {
    api
      .labVerified()
      .then(setDrills)
      .catch(() => setDrills([]))
  }, [])

  const rows = useMemo(() => {
    const byNS = new Map<string, DrillStatus>()
    for (const d of drills || []) byNS.set(d.namespace, d)
    const namespaces = new Set<string>()
    for (const s of schedules) if (s.namespace) namespaces.add(s.namespace)
    for (const d of drills || []) namespaces.add(d.namespace)

    const out: Row[] = [...namespaces].map((ns) => {
      const d = byNS.get(ns)
      if (!d) return { namespace: ns, state: 'never' as const }
      return {
        namespace: ns,
        // 'success' = lab reached ready; 'passed' = operator verdict.
        state:
          d.lastStatus === 'success' || d.lastStatus === 'passed'
            ? ('verified' as const)
            : ('failed' as const),
        lastAt: d.lastAt,
        lastSuccessAt: d.lastSuccessAt,
      }
    })
    const weight = { failed: 0, never: 1, verified: 2 }
    out.sort((a, b) => weight[a.state] - weight[b.state] || a.namespace.localeCompare(b.namespace))
    return out
  }, [schedules, drills])

  const neverCount = rows.filter((r) => r.state !== 'verified').length

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <FlaskConical className="h-4 w-4" />
          Restore verification
          {neverCount > 0 && rows.length > 0 && <Badge variant="warning">{neverCount} unproven</Badge>}
        </CardTitle>
        <CardDescription>
          Last successful restore drill per namespace —{' '}
          <Link to="/lab" className="underline-offset-2 hover:underline">
            run one in the Restore Lab
          </Link>
          .
        </CardDescription>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {drills === null ? 'Loading…' : 'No backed-up namespaces found.'}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Namespace</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="hidden sm:table-cell">Last drill</TableHead>
                <TableHead>Verified</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.namespace}>
                  <TableCell className="font-mono text-xs">{r.namespace}</TableCell>
                  <TableCell>
                    {r.state === 'verified' && <Badge variant="success">verified</Badge>}
                    {r.state === 'failed' && <Badge variant="danger">last drill failed</Badge>}
                    {r.state === 'never' && <Badge variant="warning">never drilled</Badge>}
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground sm:table-cell">
                    {r.lastAt ? formatWhen(r.lastAt) : '—'}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {r.lastSuccessAt ? formatAge(Date.now() - new Date(r.lastSuccessAt).getTime()) + ' ago' : 'never'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
