import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import {
  ChevronRight,
  Download,
  File,
  Folder,
  Home,
  Loader2,
} from 'lucide-react'
import { api, type FileNode } from '../api'
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

export default function Browser() {
  const { ns = '', name = '' } = useParams()
  const [path, setPath] = useState('/')
  const [nodes, setNodes] = useState<FileNode[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [pendingPath, setPendingPath] = useState<string | null>(null)
  const reqSeq = useRef(0)

  const crumbs = useMemo(() => breadcrumbs(path), [path])

  useEffect(() => {
    const seq = ++reqSeq.current
    const ac = new AbortController()
    setLoading(true)
    setError('')
    setPendingPath(path)
    // Clear rows immediately so navigation feels intentional, not stuck.
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
  }, [ns, name, path])

  function navigateTo(next: string) {
    const n = normalizePath(next)
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
        <Button asChild variant="outline" size="sm">
          <Link to="/snapshots">← Snapshots</Link>
        </Button>
      </div>

      {error && <Alert variant="danger">{error}</Alert>}

      <Card>
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <CardTitle className="text-sm font-normal text-muted-foreground">Path</CardTitle>
            {loading && (
              <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                Loading{pendingPath ? ` ${pendingPath}` : '…'}
              </span>
            )}
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
                      className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-primary transition-colors hover:bg-accent hover:underline"
                    >
                      {i === 0 ? <Home className="h-3.5 w-3.5" /> : null}
                      {c.label}
                    </button>
                  )}
                </span>
              )
            })}
          </nav>
        </CardHeader>
        <CardContent className="relative min-h-[12rem]">
          {loading && (
            <div
              className="pointer-events-none absolute inset-0 z-10 flex items-start justify-center bg-card/60 pt-16 backdrop-blur-[1px]"
              aria-busy="true"
              aria-live="polite"
            >
              <div className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground shadow-sm">
                <Loader2 className="h-4 w-4 animate-spin text-primary" />
                Fetching directory…
              </div>
            </div>
          )}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead className="w-24">Type</TableHead>
                <TableHead className="w-28">Size</TableHead>
                <TableHead className="w-28" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && nodes.length === 0 &&
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

              {!loading && path !== '/' && (
                <TableRow
                  className="cursor-pointer hover:bg-accent/50"
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
                    className={cn(isDir && 'cursor-pointer hover:bg-accent/50')}
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
                      {!isDir && (
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

              {!loading && nodes.length === 0 && (
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
