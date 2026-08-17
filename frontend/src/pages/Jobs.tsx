import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, type K8sObject } from '../api'
import { formatWhen } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Input } from '../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

export default function Jobs() {
  // Deep links from the dashboard land here as /jobs?namespace=<ns>.
  const [searchParams] = useSearchParams()
  const nsParam = searchParams.get('namespace') || ''
  const [jobs, setJobs] = useState<Record<string, K8sObject[] | { error: string }>>({})
  const [error, setError] = useState('')
  const [ns, setNs] = useState(nsParam)
  const [filter, setFilter] = useState(nsParam)

  const load = () => api.jobs().then(setJobs).catch((e: Error) => setError(e.message))

  useEffect(() => {
    load()
    const t = setInterval(load, 10000)
    return () => clearInterval(t)
  }, [])

  async function trigger(kind: 'backup' | 'check') {
    if (!ns) {
      setError('Enter a namespace first')
      return
    }
    if (!window.confirm(`Create on-demand ${kind} in ${ns}?`)) return
    try {
      if (kind === 'backup') await api.createBackup(ns)
      else await api.createCheck(ns)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Jobs</h1>
        <p className="text-sm text-muted-foreground">Live Backup / Restore / Check / Prune CRs</p>
      </div>
      {error && <Alert variant="danger">{error}</Alert>}
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center gap-2 space-y-0">
          <Input
            placeholder="Namespace for ad-hoc job"
            value={ns}
            onChange={(e) => setNs(e.target.value)}
            className="max-w-xs"
          />
          <Button onClick={() => trigger('backup')}>Run Backup…</Button>
          <Button variant="secondary" onClick={() => trigger('check')}>
            Run Check…
          </Button>
          <Input
            placeholder="Filter by namespace or name…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="max-w-xs sm:ml-auto"
          />
        </CardHeader>
      </Card>
      {(['backups', 'restores', 'checks', 'prunes'] as const).map((key) => {
        const val = jobs[key]
        const q = filter.trim().toLowerCase()
        const list = (Array.isArray(val) ? val : []).filter(
          (j) =>
            !q ||
            (j.namespace || '').toLowerCase().includes(q) ||
            (j.name || '').toLowerCase().includes(q),
        )
        const err = val && !Array.isArray(val) ? val.error : ''
        return (
          <Card key={key}>
            <CardHeader>
              <CardTitle className="capitalize">{key}</CardTitle>
            </CardHeader>
            <CardContent>
              {err && <Alert variant="danger">{err}</Alert>}
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Namespace</TableHead>
                    <TableHead>Name</TableHead>
                    <TableHead>Created</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.slice(0, 50).map((j) => (
                    <TableRow key={`${j.namespace}/${j.name}`}>
                      <TableCell className="font-mono text-xs">{j.namespace}</TableCell>
                      <TableCell className="font-mono text-xs">{j.name}</TableCell>
                      <TableCell className="text-muted-foreground">{formatWhen(j.creationTimestamp)}</TableCell>
                    </TableRow>
                  ))}
                  {list.length === 0 && !err && (
                    <TableRow>
                      <TableCell colSpan={3} className="text-muted-foreground">
                        None
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}
