import { NavLink, Route, Routes } from 'react-router-dom'
import { useEffect, useState, type ReactNode } from 'react'
import {
  DatabaseBackup,
  HardDrive,
  History,
  Layers3,
  Moon,
  ScrollText,
  Settings as SettingsIcon,
  Sun,
  WifiOff,
} from 'lucide-react'
import { api, type ClusterStatus, type User } from './api'
import { cn } from './lib/utils'
import { useTheme } from './theme'
import Dashboard from './pages/Dashboard'
import Snapshots from './pages/Snapshots'
import Jobs from './pages/Jobs'
import Restores from './pages/Restores'
import Audit from './pages/Audit'
import Browser from './pages/Browser'
import SnapshotDiffPage from './pages/SnapshotDiff'
import Settings from './pages/Settings'
import { Alert } from './components/ui/alert'
import { Button } from './components/ui/button'

const nav = [
  { to: '/', label: 'Dashboard', icon: Layers3, end: true },
  { to: '/snapshots', label: 'Snapshots', icon: HardDrive },
  { to: '/jobs', label: 'Jobs', icon: DatabaseBackup },
  { to: '/restores', label: 'Restores', icon: History },
  { to: '/audit', label: 'Audit', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: SettingsIcon },
]

/** Scrollable pages vs lock-to-viewport (Restores log console). */
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
        mode === 'scroll' ? 'overflow-y-auto overflow-x-hidden' : 'flex flex-col overflow-hidden',
      )}
    >
      {children}
    </div>
  )
}

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [err, setErr] = useState('')
  const [cluster, setCluster] = useState<ClusterStatus | null>(null)
  const { theme, toggle } = useTheme()

  useEffect(() => {
    api.me().then(setUser).catch((e: Error) => setErr(e.message))
  }, [])

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
    <div className="flex h-full min-h-0 overflow-hidden">
      <aside className="flex w-56 shrink-0 flex-col border-r bg-card/40 px-3 py-5">
        <div className="mb-6 flex items-start justify-between gap-2 px-2">
          <div>
            <div className="text-sm font-semibold tracking-wide">k8up btl</div>
          </div>
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
        </div>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
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
        <div className="mt-4 border-t px-2 pt-3 text-xs text-muted-foreground">
          {user?.username || '…'}
        </div>
      </aside>
      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-6 md:p-8">
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
              path="/jobs"
              element={
                <PageShell>
                  <Jobs />
                </PageShell>
              }
            />
            <Route
              path="/restores"
              element={
                <PageShell mode="fill">
                  <Restores />
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
