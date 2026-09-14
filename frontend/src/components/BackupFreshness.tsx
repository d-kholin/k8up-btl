import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { K8sObject, PVCRef } from '../api'
import { coverageResources } from '../lib/coverage'
import { formatAge, formatWhen } from '../lib/utils'
import { Alert } from './ui/alert'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

export default function BackupFreshness({
  schedules,
  snapshots,
  pvcs,
  loading,
}: {
  schedules: K8sObject[]
  snapshots: K8sObject[]
  pvcs: PVCRef[] | null
  loading: boolean
}) {
  const [showAll, setShowAll] = useState(false)
  const rows = useMemo(
    () => coverageResources(schedules, snapshots, pvcs),
    [schedules, snapshots, pvcs],
  )
  const fresh = rows.filter((r) => r.state === 'fresh').length
  const excluded = rows.filter((r) => r.state === 'excluded').length
  const attention = rows.filter((r) => !['fresh', 'excluded'].includes(r.state))
  const visible = showAll ? rows : attention.slice(0, 8)
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle>Backup coverage</CardTitle>
          <CardDescription>
            Each volume and observed SQL dump has its own recovery point.
          </CardDescription>
        </div>
        {!loading && (
          <Badge variant={attention.length || pvcs === null ? 'warning' : 'success'}>
            {fresh} / {rows.length - excluded} current
            {pvcs === null ? ' · partial inventory' : ''}
          </Badge>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {loading ? (
          <p role="status" className="text-sm text-muted-foreground">
            Loading backup coverage…
          </p>
        ) : (
          <>
            {pvcs === null && (
              <Alert variant="warning">
                Volume inventory unavailable. Coverage includes snapshot evidence only; missing
                volumes cannot be detected.
              </Alert>
            )}
            {rows.length === 0 ? (
              <p className="text-sm text-muted-foreground">No volumes or snapshot sources found.</p>
            ) : (
              <>
                {visible.length === 0 ? (
                  <p className="text-sm">No coverage issues found in the available inventory.</p>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Resource</TableHead>
                        <TableHead>Last backup</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visible.map((r) => (
                        <TableRow key={[r.namespace, r.kind, r.name].join('/')}>
                          <TableCell>
                            <div className="break-all font-medium">{r.name}</div>
                            <div className="mt-1 text-xs text-muted-foreground">
                              {r.namespace} · {r.kind}
                            </div>
                          </TableCell>
                          <TableCell
                            className="whitespace-nowrap"
                            title={
                              r.latest ? formatWhen(new Date(r.latest).toISOString()) : undefined
                            }
                          >
                            {r.latest ? formatAge(Date.now() - r.latest) + ' ago' : 'Never found'}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                r.state === 'fresh'
                                  ? 'success'
                                  : r.state === 'excluded'
                                    ? 'secondary'
                                    : r.state === 'unknown' || r.state === 'unscheduled'
                                      ? 'warning'
                                      : 'danger'
                              }
                            >
                              {
                                {
                                  fresh: 'Current',
                                  stale: 'Overdue',
                                  missing: 'No backup',
                                  unknown: 'Unknown cadence',
                                  excluded: 'Excluded',
                                  unscheduled: 'No schedule',
                                }[r.state]
                              }
                            </Badge>
                            <p className="mt-1 max-w-60 text-xs text-muted-foreground">
                              {r.reason}
                            </p>
                          </TableCell>
                          <TableCell>
                            <Button asChild variant="link" size="sm">
                              <Link
                                to={
                                  '/snapshots?namespace=' +
                                  encodeURIComponent(r.namespace) +
                                  '&q=' +
                                  encodeURIComponent(r.name)
                                }
                              >
                                Snapshots
                              </Link>
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
                <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
                  <p className="text-xs text-muted-foreground">
                    {excluded} explicitly excluded · Dumps that have never produced a snapshot are
                    not inventoried.
                    {!showAll && attention.length > 8
                      ? ' Showing 8 of ' + attention.length + ' issues.'
                      : ''}
                  </p>
                  <Button variant="outline" size="sm" onClick={() => setShowAll((v) => !v)}>
                    {showAll ? 'Show issues' : 'View all ' + rows.length + ' resources'}
                  </Button>
                </div>
              </>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
