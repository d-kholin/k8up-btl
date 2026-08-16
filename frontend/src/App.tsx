import { NavLink, Route, Routes } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { DatabaseBackup, HardDrive, History, Layers3, ScrollText } from 'lucide-react'
import { api, type User } from './api'
import { cn } from './lib/utils'
import Dashboard from './pages/Dashboard'
import Snapshots from './pages/Snapshots'
import Jobs from './pages/Jobs'
import Restores from './pages/Restores'
import Audit from './pages/Audit'
import Browser from './pages/Browser'
import { Alert } from './components/ui/alert'

const nav = [
  { to: '/', label: 'Dashboard', icon: Layers3, end: true },
  { to: '/snapshots', label: 'Snapshots', icon: HardDrive },
  { to: '/jobs', label: 'Jobs', icon: DatabaseBackup },
  { to: '/restores', label: 'Restores', icon: History },
  { to: '/audit', label: 'Audit', icon: ScrollText },
]

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.me().then(setUser).catch((e: Error) => setErr(e.message))
  }, [])

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r bg-card/40 px-3 py-5">
        <div className="mb-6 px-2">
          <div className="text-sm font-semibold tracking-wide">K8up GUI</div>
          <div className="text-xs text-muted-foreground">Backup & restore</div>
        </div>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-md px-2.5 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-foreground',
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
      <main className="flex-1 overflow-auto p-6 md:p-8">
        <div className="mx-auto max-w-6xl space-y-6">
          {err && <Alert variant="danger">{err}</Alert>}
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/snapshots" element={<Snapshots />} />
            <Route path="/snapshots/:ns/:name/browse" element={<Browser />} />
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/restores" element={<Restores />} />
            <Route path="/audit" element={<Audit />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}
