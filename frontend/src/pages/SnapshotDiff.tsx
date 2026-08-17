import { useEffect, useMemo, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { FolderSearch, GitCompareArrows, Loader2, MoveRight } from 'lucide-react'
import { api, type DiffChange, type DiffChangeKind, type K8sObject, type SnapshotDiff } from '../api'
import { formatBytes, formatWhen } from '../lib/utils'
import { snapSpec, snapTime, workloadFromPaths } from '../lib/snapshots'
import { Alert } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader } from '../components/ui/card'
import { Input } from '../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

const KIND_META: Record<DiffChangeKind, { label: string; marker: string; variant: 'success' | 'danger' | 'warning' | 'secondary' }> = {
  added: { label: 'Added', marker: '+', variant: 'success' },
  removed: { label: 'Removed', marker: '−', variant: 'danger' },
  modified: { label: 'Modified', marker: 'M', variant: 'warning' },
  metadata: { label: 'Metadata', marker: '~', variant: 'secondary' },
}

const KIND_ORDER: DiffChangeKind[] = ['added', 'removed', 'modified', 'metadata']

/** Cap rendered rows; the filter box narrows large diffs down. */
const MAX_RENDERED = 1000

function snapLabel(s: K8sObject): string {
  const spec = snapSpec(s)
  const when = formatWhen(spec.date || s.creationTimestamp)
  const wl = workloadFromPaths(spec.paths || [])
  return `${when} · ${wl}`
}

function sameWorkload(a: K8sObject, b: K8sObject): boolean {
  const pa = (snapSpec(a).paths || []).join('|')
  const pb = (snapSpec(b).paths || []).join('|')
  return pa !== '' && pa === pb
}

export default function SnapshotDiffPage() {
  const { ns = '', name = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const baseParam = searchParams.get('base') || ''

  const [snaps, setSnaps] = useState<K8sObject[]>([])
  const [snapsError, setSnapsError] = useState('')
  const [snapsLoaded, setSnapsLoaded] = useState(false)

  const [diff, setDiff] = useState<SnapshotDiff | null>(null)
  const [diffError, setDiffError] = useState('')
  const [loading, setLoading] = useState(false)

  const [filter, setFilter] = useState('')
  const [kindFilter, setKindFilter] = useState<DiffChangeKind | 'all'>('all')

  useEffect(() => {
    api
      .snapshots(ns)
      .then(setSnaps)
      .catch((e: Error) => setSnapsError(e.message))
      .finally(() => setSnapsLoaded(true))
  }, [ns])

  const target = useMemo(() => snaps.find((s) => s.name === name), [snaps, name])

  // Candidate bases: every other snapshot in the namespace, same-workload
  // first, newest first. Diffing a Postgres dump against an Immich library is
  // meaningless, so same-workload snapshots lead the list.
  const candidates = useMemo(() => {
    if (!target) return []
    const others = snaps.filter((s) => s.name !== name)
    const same = others.filter((s) => sameWorkload(s, target)).sort((a, b) => snapTime(b) - snapTime(a))
    const rest = others.filter((s) => !sameWorkload(s, target)).sort((a, b) => snapTime(b) - snapTime(a))
    return [...same, ...rest]
  }, [snaps, target, name])

  // Default base: the closest older same-workload snapshot.
  useEffect(() => {
    if (baseParam || !target || candidates.length === 0) return
    const older = candidates.filter((s) => sameWorkload(s, target) && snapTime(s) < snapTime(target))
    const pick = older[0] || candidates[0]
    if (pick?.name) setSearchParams({ base: pick.name }, { replace: true })
  }, [baseParam, target, candidates, setSearchParams])

  useEffect(() => {
    if (!baseParam) return
    const ac = new AbortController()
    setLoading(true)
    setDiff(null)
    setDiffError('')
    api
      .diff(ns, name, baseParam, ac.signal)
      .then(setDiff)
      .catch((e: Error) => {
        if (e.name !== 'AbortError') setDiffError(e.message)
      })
      .finally(() => setLoading(false))
    return () => ac.abort()
  }, [ns, name, baseParam])

  const counts = useMemo(() => {
    const c: Record<DiffChangeKind, number> = { added: 0, removed: 0, modified: 0, metadata: 0 }
    for (const ch of diff?.changes || []) c[ch.kind] = (c[ch.kind] || 0) + 1
    return c
  }, [diff])

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return (diff?.changes || []).filter(
      (c) => (kindFilter === 'all' || c.kind === kindFilter) && (!q || c.path.toLowerCase().includes(q)),
    )
  }, [diff, filter, kindFilter])

  const baseSnap = candidates.find((s) => s.name === baseParam)
  const baseIsNewer = !!(target && baseSnap && snapTime(baseSnap) > snapTime(target))

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <GitCompareArrows className="h-6 w-6 text-primary" />
            Compare snapshots
          </h1>
          <p className="font-mono text-sm text-muted-foreground">
            {ns}/{name}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild variant="secondary" size="sm">
            <Link to={`/snapshots/${ns}/${name}/browse`}>
              <FolderSearch className="h-3.5 w-3.5" />
              Browse
            </Link>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link to="/snapshots">← Snapshots</Link>
          </Button>
        </div>
      </div>

      {snapsError && <Alert variant="danger">{snapsError}</Alert>}
      {snapsLoaded && !target && !snapsError && (
        <Alert variant="danger">Snapshot {name} not found in namespace {ns}.</Alert>
      )}

      <Card>
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <div className="min-w-0 flex-1 sm:max-w-md">
              <label htmlFor="diff-base" className="mb-1 block text-xs text-muted-foreground">
                Compare against (base)
              </label>
              <select
                id="diff-base"
                value={baseParam}
                onChange={(e) => setSearchParams({ base: e.target.value }, { replace: true })}
                className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                disabled={candidates.length === 0}
              >
                {candidates.length === 0 && <option value="">No other snapshots in {ns}</option>}
                {candidates.map((s) => (
                  <option key={s.name} value={s.name}>
                    {snapLabel(s)}
                    {target && !sameWorkload(s, target) ? ' (different workload)' : ''}
                  </option>
                ))}
              </select>
            </div>
            <div className="hidden items-center gap-2 pt-4 text-sm text-muted-foreground sm:flex">
              <span className="font-mono text-xs">{baseSnap ? formatWhen(snapSpec(baseSnap).date || baseSnap.creationTimestamp) : 'base'}</span>
              <MoveRight className="h-4 w-4" />
              <span className="font-mono text-xs">
                {target ? formatWhen(snapSpec(target).date || target.creationTimestamp) : 'this snapshot'}
              </span>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Shows what changed from the base snapshot to this one (restic diff, read-only).
            {baseIsNewer && ' Note: the selected base is newer than this snapshot, so added/removed are reversed.'}
          </p>
        </CardHeader>
      </Card>

      {diffError && <Alert variant="danger">{diffError}</Alert>}

      {loading && (
        <Card>
          <CardContent className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin text-primary" />
            Comparing snapshots — first diff of a pair reads repo metadata and can take a moment…
          </CardContent>
        </Card>
      )}

      {diff && !loading && (
        <>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded-lg border border-border bg-card p-3">
              <div className="text-xs text-muted-foreground">Added</div>
              <div className="text-lg font-semibold text-emerald-700 dark:text-emerald-400">
                {counts.added.toLocaleString()}
              </div>
              <div className="text-xs text-muted-foreground">{formatBytes(diff.added.bytes)} new data</div>
            </div>
            <div className="rounded-lg border border-border bg-card p-3">
              <div className="text-xs text-muted-foreground">Removed</div>
              <div className="text-lg font-semibold text-red-700 dark:text-red-400">
                {counts.removed.toLocaleString()}
              </div>
              <div className="text-xs text-muted-foreground">{formatBytes(diff.removed.bytes)} deleted</div>
            </div>
            <div className="rounded-lg border border-border bg-card p-3">
              <div className="text-xs text-muted-foreground">Modified</div>
              <div className="text-lg font-semibold text-amber-700 dark:text-amber-400">
                {counts.modified.toLocaleString()}
              </div>
              <div className="text-xs text-muted-foreground">content changed</div>
            </div>
            <div className="rounded-lg border border-border bg-card p-3">
              <div className="text-xs text-muted-foreground">Metadata only</div>
              <div className="text-lg font-semibold">{counts.metadata.toLocaleString()}</div>
              <div className="text-xs text-muted-foreground">times/permissions</div>
            </div>
          </div>

          {diff.truncated && (
            <Alert variant="warning">
              Large diff: showing the first {diff.changes.length.toLocaleString()} of{' '}
              {diff.totalChanges.toLocaleString()} changed paths. Counts above reflect the shown subset.
            </Alert>
          )}

          <Card>
            <CardHeader className="flex flex-row flex-wrap items-center gap-3 space-y-0">
              <Input
                placeholder="Filter paths…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                className="max-w-sm"
              />
              <div className="flex flex-wrap gap-1.5">
                <Button
                  type="button"
                  size="sm"
                  variant={kindFilter === 'all' ? 'secondary' : 'ghost'}
                  onClick={() => setKindFilter('all')}
                >
                  All ({(diff.changes || []).length.toLocaleString()})
                </Button>
                {KIND_ORDER.map((k) =>
                  counts[k] > 0 ? (
                    <Button
                      key={k}
                      type="button"
                      size="sm"
                      variant={kindFilter === k ? 'secondary' : 'ghost'}
                      onClick={() => setKindFilter(kindFilter === k ? 'all' : k)}
                    >
                      {KIND_META[k].label} ({counts[k].toLocaleString()})
                    </Button>
                  ) : null,
                )}
              </div>
            </CardHeader>
            <CardContent>
              {diff.changes.length === 0 ? (
                <div className="flex flex-col items-center gap-1 py-10 text-center">
                  <Badge variant="success">No changes</Badge>
                  <p className="mt-2 text-sm text-muted-foreground">
                    These two snapshots contain identical file content.
                  </p>
                </div>
              ) : (
                <>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-28">Change</TableHead>
                        <TableHead>Path</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {filtered.slice(0, MAX_RENDERED).map((c: DiffChange) => (
                        <TableRow key={`${c.kind}:${c.path}`}>
                          <TableCell>
                            <Badge variant={KIND_META[c.kind].variant} title={`restic modifier: ${c.modifier}`}>
                              <span className="mr-1 font-mono">{KIND_META[c.kind].marker}</span>
                              {KIND_META[c.kind].label}
                            </Badge>
                          </TableCell>
                          <TableCell className="break-all font-mono text-xs">{c.path}</TableCell>
                        </TableRow>
                      ))}
                      {filtered.length === 0 && (
                        <TableRow>
                          <TableCell colSpan={2} className="text-muted-foreground">
                            No changes match the filter.
                          </TableCell>
                        </TableRow>
                      )}
                    </TableBody>
                  </Table>
                  {filtered.length > MAX_RENDERED && (
                    <p className="mt-3 text-xs text-muted-foreground">
                      Showing first {MAX_RENDERED.toLocaleString()} of {filtered.length.toLocaleString()} matching
                      paths — refine the filter to narrow down.
                    </p>
                  )}
                </>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  )
}
