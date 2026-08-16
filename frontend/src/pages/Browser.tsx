import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import {
  ChevronRight,
  Download,
  File,
  Folder,
  FolderDown,
  Home,
  Loader2,
  Package,
} from 'lucide-react'
import { api, type FileNode, type K8sObject } from '../api'
import { cn, formatBytes } from '../lib/utils'
import { Alert } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'

type Crumb = { label: string; path: string }

function breadcrumbs(path: string): Crumb[] {
  const clean = path === '/' ? '/' : path.replace(/\/+$/, '') || '/'
  const crumbs: Crumb[] = [{ label: 'root', path: '/' }]
  if (clean === '/') return crumbs
  const parts = clean.split('/').filter(Boolean)
  let acc = ''
  for (const part of parts) {
    acc += `/${part}`
    crumbs.push({ label: part, path: acc })
  }
  return crumbs
}

function normalizePath(p: string): string {
  if (!p || p === '/') return '/'
  const withSlash = p.startsWith('/') ? p : `/${p}`
  return withSlash.replace(/\/+$/, '') || '/'
}

/** True when last segment looks like a file (has an extension). */
function looksLikeFilePath(p: string): boolean {
  const base = p.split('/').filter(Boolean).pop() || ''
  if (!base || base.startsWith('.')) return false
  const dot = base.lastIndexOf('.')
  return dot > 0 && dot < base.length - 1
}

/**
 * Prefer snapshot spec.paths (K8up backup roots, often /data/<pvc>).
 * Cap at two path segments — that's where workload data usually lives.
 * File paths land on their parent so the file is visible in the listing.
 */
export function defaultPathFromSnapshotPaths(paths: string[] | undefined): string | null {
  if (!paths?.length) return null
  let p = normalizePath(paths[0])
  if (looksLikeFilePath(p)) {
    const parent = p.replace(/\/?[^/]+$/, '') || '/'
    return parent === p ? '/' : parent
  }
  const parts = p.split('/').filter(Boolean)
  if (parts.length === 0) return null
  if (parts.length > 2) {
    return `/${parts.slice(0, 2).join('/')}`
  }
  return p
}

function snapPaths(snap: K8sObject | undefined): string[] {
  const spec = snap?.spec as { paths?: string[] } | undefined
  return Array.isArray(spec?.paths) ? spec!.paths! : []
}

/** Walk single-child directory chain up to maxDepth (fallback when paths missing). */
async function descendSingleChildDirs(
  ns: string,
  name: string,
  maxDepth: number,
  signal?: AbortSignal,
): Promise<string> {
  let cur = '/'
  for (let i = 0; i < maxDepth; i++) {
    const nodes = await api.files(ns, name, cur, signal)
    const dirs = nodes.filter((n) => n.type === 'dir')
    // Only auto-enter when the listing is a single directory (pure nesting).
    if (dirs.length !== 1 || nodes.length !== 1) break
    const next = normalizePath(dirs[0].path || `${cur === '/' ? '' : cur}/${dirs[0].name}`)
    if (next === cur) break
    cur = next
  }
  return cur
}

async function resolveInitialBrowsePath(
  ns: string,
  name: string,
  signal?: AbortSignal,
): Promise<string> {
  try {
    const snaps = await api.snapshots(ns)
    const snap = snaps.find((s) => s.name === name)
    const fromSpec = defaultPathFromSnapshotPaths(snapPaths(snap))
    if (fromSpec && fromSpec !== '/') {
      return fromSpec
    }
  } catch {
    // fall through to tree walk
  }
  try {
    return await descendSingleChildDirs(ns, name, 2, signal)
  } catch {
    return '/'
  }
}

export default function Browser() {
  const { ns = '', name = '' } = useParams()
  const [path, setPath] = useState('/')
  const [nodes, setNodes] = useState<FileNode[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [pendingPath, setPendingPath] = useState<string | null>(null)
  const [bootstrapping, setBootstrapping] = useState(true)
  const reqSeq = useRef(0)
  /** After user navigates, stop re-applying the default data root. */
  const userNavigated = useRef(false)

  const crumbs = useMemo(() => breadcrumbs(path), [path])
  const folderZipUrl = useMemo(
    () => api.downloadUrl(ns, name, path, { archive: 'zip', folder: true }),
    [ns, name, path],
  )
  const snapshotZipUrl = useMemo(
    () => api.downloadUrl(ns, name, '/', { archive: 'zip', folder: true }),
    [ns, name],
  )

  // Once per snapshot: land on data root (spec.paths or 2-level single-dir walk).
  useEffect(() => {
    userNavigated.current = false
    setBootstrapping(true)
    setPath('/')
    const ac = new AbortController()
    let cancelled = false
    resolveInitialBrowsePath(ns, name, ac.signal)
      .then((p) => {
        if (cancelled || userNavigated.current) return
        setPath(p)
      })
      .catch((e: Error) => {
        if (cancelled || e.name === 'AbortError') return
        if (!userNavigated.current) setPath('/')
      })
      .finally(() => {
        if (!cancelled) setBootstrapping(false)
      })
    return () => {
      cancelled = true
      ac.abort()
    }
  }, [ns, name])

  useEffect(() => {
    // Avoid listing `/` only to immediately jump; still list once bootstrap settles.
    if (bootstrapping && path === '/') return

    const seq = ++reqSeq.current
    const ac = new AbortController()
    setLoading(true)
    setError('')
    setPendingPath(path)
    setNodes([])

    api
      .files(ns, name, path, ac.signal)
      .then((list) => {
        if (seq !== reqSeq.current) return
        setNodes(list)
      })
      .catch((e: Error) => {
        if (seq !== reqSeq.current) return
        if (e.name === 'AbortError') return
        setError(e.message)
        setNodes([])
      })
      .finally(() => {
        if (seq !== reqSeq.current) return
        setLoading(false)
        setPendingPath(null)
      })

    return () => {
      ac.abort()
    }
  }, [ns, name, path, bootstrapping])

  function navigateTo(next: string) {
    const n = normalizePath(next)
    userNavigated.current = true
    setBootstrapping(false)
    if (n === path && !loading) return
    setPath(n)
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Browse snapshot</h1>
          <p className="font-mono text-sm text-muted-foreground">
            {ns}/{name}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild variant="secondary" size="sm">
            <a href={snapshotZipUrl} download title="Download entire snapshot as .zip via restic dump">
              <Package className="h-3.5 w-3.5" />
              Snapshot .zip
            </a>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link to="/snapshots">← Snapshots</Link>
          </Button>
        </div>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}

      <Card>
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle className="text-sm font-normal text-muted-foreground">Path</CardTitle>
            <div className="flex flex-wrap items-center gap-2">
              {(loading || bootstrapping) && (
                <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {bootstrapping ? 'Opening data path…' : `Loading${pendingPath ? ` ${pendingPath}` : '…'}`}
                </span>
              )}
              <Button asChild size="sm" variant="secondary">
                <a
                  href={folderZipUrl}
                  download
                  title={
                    path === '/'
                      ? 'Download whole snapshot as zip'
                      : `Download folder ${path} as zip`
                  }
                >
                  <FolderDown className="h-3.5 w-3.5" />
                  {path === '/' ? 'Download snapshot .zip' : 'Download folder .zip'}
                </a>
              </Button>
            </div>
          </div>
          <nav
            aria-label="Breadcrumb"
            className="flex flex-wrap items-center gap-0.5 rounded-md border border-border bg-muted/40 px-2 py-1.5 font-mono text-xs"
          >
            {crumbs.map((c, i) => {
              const isLast = i === crumbs.length - 1
              return (
                <span key={c.path} className="inline-flex items-center gap-0.5">
                  {i > 0 && <ChevronRight className="mx-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
                  {isLast ? (
                    <span
                      className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium text-foreground"
                      aria-current="page"
                    >
                      {i === 0 ? <Home className="h-3.5 w-3.5" /> : null}
                      {c.label}
                    </span>
                  ) : (
                    <button
                      type="button"
                      onClick={() => navigateTo(c.path)}
                      className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-primary transition-colors hover:bg-row-hover hover:underline"
                    >
                      {i === 0 ? <Home className="h-3.5 w-3.5" /> : null}
                      {c.label}
                    </button>
                  )}
                </span>
              )
            })}
          </nav>
          <p className="text-xs text-muted-foreground">
            Opens at the backup data path (usually two levels down). Use breadcrumbs or .. to go up. Folders/snapshots
            download as zip streams from restic (read-only).
          </p>
        </CardHeader>
        <CardContent className="relative min-h-[12rem]">
          {(loading || bootstrapping) && (
            <div
              className="pointer-events-none absolute inset-0 z-10 flex items-start justify-center bg-card/60 pt-16 backdrop-blur-[1px]"
              aria-busy="true"
              aria-live="polite"
            >
              <div className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground shadow-sm">
                <Loader2 className="h-4 w-4 animate-spin text-primary" />
                {bootstrapping ? 'Finding data path…' : 'Fetching directory…'}
              </div>
            </div>
          )}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead className="w-24">Type</TableHead>
                <TableHead className="w-28">Size</TableHead>
                <TableHead className="w-36" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(loading || bootstrapping) &&
                nodes.length === 0 &&
                Array.from({ length: 6 }).map((_, i) => (
                  <TableRow key={`sk-${i}`} className="animate-pulse">
                    <TableCell>
                      <div className="h-4 w-48 rounded bg-muted" />
                    </TableCell>
                    <TableCell>
                      <div className="h-4 w-12 rounded bg-muted" />
                    </TableCell>
                    <TableCell>
                      <div className="h-4 w-16 rounded bg-muted" />
                    </TableCell>
                    <TableCell />
                  </TableRow>
                ))}

              {!loading && !bootstrapping && path !== '/' && (
                <TableRow
                  className="cursor-pointer hover:bg-row-hover"
                  onClick={() => {
                    const parent = path.replace(/\/?[^/]+$/, '') || '/'
                    navigateTo(parent)
                  }}
                >
                  <TableCell className="font-mono text-xs">
                    <span className="inline-flex items-center gap-2 text-muted-foreground">
                      <Folder className="h-4 w-4" />
                      ..
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground">dir</TableCell>
                  <TableCell />
                  <TableCell />
                </TableRow>
              )}

              {nodes.map((n) => {
                const isDir = n.type === 'dir'
                const full = normalizePath(n.path || `/${n.name}`)
                return (
                  <TableRow
                    key={full}
                    className={cn(isDir && 'cursor-pointer hover:bg-row-hover')}
                    onClick={isDir ? () => navigateTo(full) : undefined}
                  >
                    <TableCell className="font-mono text-xs">
                      {isDir ? (
                        <span className="inline-flex items-center gap-2 text-primary">
                          <Folder className="h-4 w-4 shrink-0" />
                          <span className="hover:underline">{n.name || n.path}/</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-2">
                          <File className="h-4 w-4 shrink-0 text-muted-foreground" />
                          {n.name || n.path}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{n.type || '—'}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {isDir ? '—' : formatBytes(n.size)}
                    </TableCell>
                    <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                      {isDir ? (
                        <a
                          className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
                          href={api.downloadUrl(ns, name, full, { archive: 'zip', folder: true })}
                          download
                          title="Download folder as zip"
                        >
                          <FolderDown className="h-3.5 w-3.5" />
                          Zip
                        </a>
                      ) : (
                        <a
                          className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
                          href={api.downloadUrl(ns, name, n.path || n.name)}
                          download
                        >
                          <Download className="h-3.5 w-3.5" />
                          Download
                        </a>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}

              {!loading && !bootstrapping && nodes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    Empty or unavailable.
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
