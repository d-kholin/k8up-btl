import { NavLink, Route, Routes } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { api, type User } from './api'
import Dashboard from './pages/Dashboard'
import Snapshots from './pages/Snapshots'
import Jobs from './pages/Jobs'
import Restores from './pages/Restores'
import Audit from './pages/Audit'
import Browser from './pages/Browser'

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.me().then(setUser).catch((e: Error) => setErr(e.message))
  }, [])

  return (
    <div className="layout">
      <nav>
        <div className="brand">K8up GUI</div>
        <NavLink to="/" end>Dashboard</NavLink>
        <NavLink to="/snapshots">Snapshots</NavLink>
        <NavLink to="/jobs">Jobs</NavLink>
        <NavLink to="/restores">Restores</NavLink>
        <NavLink to="/audit">Audit</NavLink>
        <div style={{ flex: 1 }} />
        <div className="muted" style={{ fontSize: '0.85rem', padding: '0.5rem' }}>
          {user ? user.username : '…'}
        </div>
      </nav>
      <main>
        {err && <div className="error card">{err}</div>}
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/snapshots" element={<Snapshots />} />
          <Route path="/snapshots/:ns/:name/browse" element={<Browser />} />
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/restores" element={<Restores />} />
          <Route path="/audit" element={<Audit />} />
        </Routes>
      </main>
    </div>
  )
}
