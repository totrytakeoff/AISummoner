import { Navigate, Route, Routes, useParams } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider, PublicOnly, RequireAuth } from './auth/AuthContext'
import { AppShell } from './components/AppShell'
import { DevicesPage } from './pages/DevicesPage'
import { LoginPage } from './pages/LoginPage'
import { WorkspacePage } from './pages/WorkspacePage'

function Protected({ children }: { children: ReactNode }) {
  return <RequireAuth><AppShell>{children}</AppShell></RequireAuth>
}

function WorkspaceRedirect({ terminal = false, settings = false }: { terminal?: boolean; settings?: boolean }) {
  const { deviceId } = useParams()
  if (!deviceId) return <Navigate to="/devices" replace />
  const query = terminal ? '?dock=terminal' : settings ? '?settings=device' : ''
  const path = `/devices/${encodeURIComponent(deviceId)}/workspace${query}`
  return <Navigate to={path} replace />
}

export function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<PublicOnly><LoginPage /></PublicOnly>} />
        <Route path="/devices" element={<Protected><DevicesPage /></Protected>} />
        <Route path="/devices/:deviceId" element={<Protected><WorkspaceRedirect settings /></Protected>} />
        <Route path="/devices/:deviceId/workspace" element={<Protected><WorkspacePage /></Protected>} />
        <Route path="/devices/:deviceId/terminal" element={<Protected><WorkspaceRedirect terminal /></Protected>} />
        <Route path="/devices/:deviceId/agent" element={<Protected><WorkspaceRedirect /></Protected>} />
        <Route path="/" element={<Navigate to="/devices" replace />} />
        <Route path="*" element={<Navigate to="/devices" replace />} />
      </Routes>
    </AuthProvider>
  )
}
