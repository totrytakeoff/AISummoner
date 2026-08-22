import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'

export function AppShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  return (
    <div className="app-shell">
      <header className="topbar">
        <Link className="brand" to="/devices" aria-label="AISummoner devices">
          <span className="brand-mark" aria-hidden="true">A</span>
          <span>AISummoner</span>
        </Link>
        <nav aria-label="Primary navigation">
          <NavLink to="/devices">Devices</NavLink>
        </nav>
        <div className="account">
          <span>{user?.username}</span>
          <button className="button ghost small" type="button" onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>
      <main className="page-shell">{children}</main>
    </div>
  )
}
