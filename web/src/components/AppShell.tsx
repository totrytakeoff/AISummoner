import type { ReactNode } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import { AISummonerMark } from './Icons'

export function AppShell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const location = useLocation()
  const workspace = /^\/devices\/[^/]+\/workspace$/.test(location.pathname)
  if (workspace) {
    return <div className="app-shell workspace-app-shell"><div className="page-shell workspace-shell">{children}</div></div>
  }
  return (
    <div className="app-shell">
      <header className="topbar">
        <Link className="brand" to="/devices" aria-label="AISummoner 设备">
          <span className="brand-mark" aria-hidden="true"><AISummonerMark /></span><span>AISummoner</span>
        </Link>
        <nav aria-label="主导航">
          <NavLink to="/devices">设备</NavLink>
        </nav>
        <div className="account">
          <span>{user?.username}</span>
          <button className="button ghost small" type="button" onClick={() => void logout()}>
            退出登录
          </button>
        </div>
      </header>
      <main className={`page-shell${workspace ? ' workspace-shell' : ''}`}>{children}</main>
    </div>
  )
}
