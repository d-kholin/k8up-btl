import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { FlaskConical } from 'lucide-react'
import { api, type DrillStatus, type K8sObject } from '../api'
import { formatAge, formatWhen } from '../lib/utils'
import { Badge } from './ui/badge'
import { Alert } from './ui/alert'
import { Button } from './ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

type Row = {
  namespace: string
  state: 'verified' | 'untested' | 'failed' | 'never'
  lastAt?: string
  lastPassedAt?: string
}

/** "Last verified restore per app" — the evidence panel restore drills exist
 * for: every backed-up namespace vs its most recent Restore Lab drill. */
export default function RestoreVerification({ schedules }: { schedules: K8sObject[] }) {
  const [drills, setDrills] = useState<DrillStatus[] | null>(null)
  const [error, setError] = useState('')
  const load = () =>
    api
      .labVerified()
      .then((value) => {
        setDrills(value)
        setError('')
      })
      .catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const timer = setInterval(load, 60000)
    return () => clearInterval(timer)
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
        // Only the operator's pass verdict verifies a restore; 'restored'
        // (and legacy 'success') means the lab came up but nobody judged it.
        state:
          d.lastStatus === 'passed'
            ? ('verified' as const)
            : d.lastStatus === 'restored' || d.lastStatus === 'success'
              ? ('untested' as const)
              : ('failed' as const),
        lastAt: d.lastAt,
        lastPassedAt: d.lastPassedAt,
      }
    })
    const weight = { failed: 0, never: 1, untested: 2, verified: 3 }
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
          {drills !== null && !error && neverCount > 0 && rows.length > 0 && (
            <Badge variant="warning">{neverCount} unproven</Badge>
          )}
        </CardTitle>
        <CardDescription>
          Last operator-passed restore drill per namespace —{' '}
          <Link to="/lab" className="underline-offset-2 hover:underline">
            run one in the Restore Lab
          </Link>
          .
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error ? (
          <Alert variant="warning">
            Verification unavailable. {error}{' '}
            <Button variant="outline" size="sm" onClick={load}>
              Retry
            </Button>
          </Alert>
        ) : drills === null ? (
          <p role="status" className="text-sm text-muted-foreground">
            Loading restore verification…
          </p>
        ) : rows.length === 0 ? (
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
                <TableHead>Last passed</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.namespace}>
                  <TableCell className="font-mono text-xs">{r.namespace}</TableCell>
                  <TableCell>
                    {r.state === 'verified' && <Badge variant="success">verified</Badge>}
                    {r.state === 'untested' && (
                      <Badge variant="warning">restored — not tested</Badge>
                    )}
                    {r.state === 'failed' && <Badge variant="danger">last drill failed</Badge>}
                    {r.state === 'never' && <Badge variant="warning">never drilled</Badge>}
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground sm:table-cell">
                    {r.lastAt ? formatWhen(r.lastAt) : '—'}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {r.lastPassedAt
                      ? formatAge(Date.now() - new Date(r.lastPassedAt).getTime()) + ' ago'
                      : 'never'}
                  </TableCell>
                  <TableCell>
                    <Button asChild variant="link" size="sm">
                      <Link to={'/lab?namespace=' + encodeURIComponent(r.namespace)}>
                        Test restore
                      </Link>
                    </Button>
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
