import { useEffect, useState } from 'react'
import { api, type RestoreState } from '../api'
import { formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

export default function Restores() {
  const [items, setItems] = useState<RestoreState[]>([])
  const [error, setError] = useState('')

  const load = () => api.restores().then(setItems).catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const es = new EventSource('/api/v1/events')
    es.onmessage = () => load()
    const t = setInterval(load, 5000)
    return () => {
      es.close()
      clearInterval(t)
    }
  }, [])

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Restore operations</h1>
        <p className="text-sm text-muted-foreground">In-process restore jobs from this GUI backend</p>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}
      <Card>
        <CardHeader>
          <CardTitle>Recent</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Step</TableHead>
                <TableHead>PVC</TableHead>
                <TableHead>Started</TableHead>
                <TableHead>Result</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.restoreId}>
                  <TableCell className="font-mono text-xs">{r.restoreId.slice(0, 8)}</TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        r.step === 'done' ? 'success' : r.step === 'failed' ? 'danger' : 'warning'
                      }
                    >
                      {r.step}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {r.pvcNamespace}/{r.pvcName}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatWhen(r.startedAt)}</TableCell>
                  <TableCell className="text-sm">
                    {r.step === 'done' && r.argoSyncResumed !== false && (
                      <Badge variant="success">complete, Argo resumed</Badge>
                    )}
                    {r.step === 'failed' && r.argoSyncResumed && (
                      <Badge variant="warning">failed, Argo resumed</Badge>
                    )}
                    {r.step === 'failed' && r.argoSyncResumed === false && (
                      <Badge variant="danger">failed, Argo still stopped</Badge>
                    )}
                    {r.lastError && <div className="mt-1 text-xs text-red-400">{r.lastError}</div>}
                  </TableCell>
                  <TableCell>
                    {r.argoSyncResumed === false && (
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={() =>
                          api
                            .resumeArgo('argocd', 'application-controller')
                            .then(load)
                            .catch((e: Error) => setError(e.message))
                        }
                      >
                        Resume Argo
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    No restore jobs in this backend process yet.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
