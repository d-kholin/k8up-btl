import { Navigate, NavLink, Route, Routes, useLocation, useSearchParams } from 'react-router-dom'
import { useEffect, useState, type ReactNode } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  DatabaseBackup,
  FlaskConical,
  HardDrive,
  History,
  Layers3,
  Menu,
  Moon,
  ScrollText,
  Settings as SettingsIcon,
  Sun,
  WifiOff,
  X,
} from 'lucide-react'
import { api, type ClusterStatus, type User } from './api'
import { cn } from './lib/utils'
import { useTheme } from './theme'
import Dashboard from './pages/Dashboard'
import Snapshots from './pages/Snapshots'
import Workloads from './pages/Workloads'
import WorkloadDetail from './pages/WorkloadDetail'
import Restores from './pages/Restores'
import Lab from './pages/Lab'
import Audit from './pages/Audit'
import Browser from './pages/Browser'
import SnapshotDiffPage from './pages/SnapshotDiff'
import Settings from './pages/Settings'
import { Alert } from './components/ui/alert'
import { Button } from './components/ui/button'

const nav = [
  { to: '/', label: 'Dashboard', icon: Layers3, end: true },
  { to: '/snapshots', label: 'Snapshots', icon: HardDrive },
  { to: '/workloads', label: 'Workloads', icon: DatabaseBackup },
  { to: '/restores', label: 'Restores', icon: History },
  { to: '/lab', label: 'Restore Lab', icon: FlaskConical },
  { to: '/audit', label: 'Audit', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: SettingsIcon },
]

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav className="flex flex-1 flex-col gap-1">
      {nav.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              'flex items-center gap-2 rounded-md px-2.5 py-2 text-sm text-muted-foreground transition-colors hover:bg-row-hover hover:text-row-hover-foreground',
              isActive && 'bg-accent font-medium text-accent-foreground',
            )
          }
        >
          <item.icon className="h-4 w-4" />
          {item.label}
        </NavLink>
      ))}
    </nav>
  )
}

function ThemeToggle() {
  const { theme, toggle } = useTheme()
  return (
    <Button
      type="button"
      size="icon"
      variant="ghost"
      className="h-8 w-8 shrink-0"
      onClick={toggle}
      title={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
    >
      {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </Button>
  )
}

/** Mobile slide-over nav (Radix dialog for focus trap / escape / scroll lock). */
function MobileNav({
  open,
  onOpenChange,
  user,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  user: User | null
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm md:hidden" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed inset-y-0 left-0 z-50 flex w-64 max-w-[80vw] flex-col border-r bg-card px-3 py-5 shadow-lg md:hidden"
        >
          <div className="mb-6 flex items-center justify-between px-2">
            <DialogPrimitive.Title className="text-sm font-semibold tracking-wide">
              k8up btl
            </DialogPrimitive.Title>
            <DialogPrimitive.Close asChild>
              <Button type="button" size="icon" variant="ghost" className="h-8 w-8">
                <X className="h-4 w-4" />
                <span className="sr-only">Close menu</span>
              </Button>
            </DialogPrimitive.Close>
          </div>
          <NavLinks onNavigate={() => onOpenChange(false)} />
          <div className="mt-4 border-t px-2 pt-3 text-xs text-muted-foreground">
            {user?.username || '…'}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/** Scrollable pages vs lock-to-viewport (Restores log console — desktop only;
 * on small screens fill-mode pages scroll like everything else). */
function PageShell({
  children,
  mode = 'scroll',
}: {
  children: ReactNode
  mode?: 'scroll' | 'fill'
}) {
  return (
    <div
      className={cn(
        'min-h-0 w-full flex-1',
        mode === 'scroll'
          ? 'overflow-y-auto overflow-x-hidden'
          : 'flex flex-col overflow-y-auto overflow-x-hidden lg:overflow-hidden',
      )}
    >
      {children}
    </div>
  )
}

function LegacyJobsRedirect() {
  const [params] = useSearchParams()
  const ns = params.get('namespace')
  return <Navigate to={ns ? `/workloads/${encodeURIComponent(ns)}` : '/workloads'} replace />
}

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [err, setErr] = useState('')
  const [cluster, setCluster] = useState<ClusterStatus | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const location = useLocation()

  useEffect(() => {
    api.me().then(setUser).catch((e: Error) => setErr(e.message))
  }, [])

  // Belt-and-braces: close the drawer on any route change (back button etc.).
  useEffect(() => {
    setMenuOpen(false)
  }, [location.pathname])

  // Surface degraded mode: without this, a dead cluster connection renders as
  // "no snapshots" everywhere.
  useEffect(() => {
    const check = () =>
      api
        .meta()
        .then((m) => setCluster(m.cluster ?? null))
        .catch(() => setCluster({ connected: false, error: 'backend unreachable' }))
    check()
    const t = setInterval(check, 30000)
    return () => clearInterval(t)
  }, [])

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden md:flex-row">
      {/* Mobile top bar */}
      <header className="flex shrink-0 items-center justify-between border-b bg-card/40 px-3 py-2 md:hidden">
        <div className="flex items-center gap-1">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="h-9 w-9"
            onClick={() => setMenuOpen(true)}
          >
            <Menu className="h-5 w-5" />
            <span className="sr-only">Open menu</span>
          </Button>
          <span className="text-sm font-semibold tracking-wide">k8up btl</span>
        </div>
        <ThemeToggle />
      </header>
      <MobileNav open={menuOpen} onOpenChange={setMenuOpen} user={user} />

      {/* Desktop sidebar */}
      <aside className="hidden w-56 shrink-0 flex-col border-r bg-card/40 px-3 py-5 md:flex">
        <div className="mb-6 flex items-start justify-between gap-2 px-2">
          <div>
            <div className="text-sm font-semibold tracking-wide">k8up btl</div>
          </div>
          <ThemeToggle />
        </div>
        <NavLinks />
        <div className="mt-4 border-t px-2 pt-3 text-xs text-muted-foreground">
          {user?.username || '…'}
        </div>
      </aside>

      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-4 md:p-8">
        <div className="mx-auto flex min-h-0 w-full max-w-6xl flex-1 flex-col">
          {cluster && !cluster.connected && (
            <div className="mb-4 shrink-0">
              <Alert variant="danger">
                <span className="flex items-center gap-2">
                  <WifiOff className="h-4 w-4 shrink-0" />
                  <span>
                    Kubernetes API unreachable — data shown may be empty or stale.
                    {cluster.error ? ` (${cluster.error})` : ''}
                  </span>
                </span>
              </Alert>
            </div>
          )}
          {err && (
            <div className="mb-4 shrink-0">
              <Alert variant="danger">{err}</Alert>
            </div>
          )}
          <Routes>
            <Route
              path="/"
              element={
                <PageShell>
                  <Dashboard />
                </PageShell>
              }
            />
            <Route
              path="/snapshots"
              element={
                <PageShell>
                  <Snapshots />
                </PageShell>
              }
            />
            <Route
              path="/snapshots/:ns/:name/browse"
              element={
                <PageShell>
                  <Browser />
                </PageShell>
              }
            />
            <Route
              path="/snapshots/:ns/:name/diff"
              element={
                <PageShell>
                  <SnapshotDiffPage />
                </PageShell>
              }
            />
            <Route
              path="/workloads"
              element={
                <PageShell>
                  <Workloads />
                </PageShell>
              }
            />
            <Route
              path="/workloads/:ns"
              element={
                <PageShell>
                  <WorkloadDetail />
                </PageShell>
              }
            />
            {/* Pre-Workloads deep links (dashboard bookmarks, notifications). */}
            <Route path="/jobs" element={<LegacyJobsRedirect />} />
            <Route
              path="/restores"
              element={
                <PageShell mode="fill">
                  <Restores />
                </PageShell>
              }
            />
            <Route
              path="/lab"
              element={
                <PageShell>
                  <Lab />
                </PageShell>
              }
            />
            <Route
              path="/audit"
              element={
                <PageShell>
                  <Audit />
                </PageShell>
              }
            />
            <Route
              path="/settings"
              element={
                <PageShell>
                  <Settings />
                </PageShell>
              }
            />
          </Routes>
        </div>
      </main>
    </div>
  )
}
