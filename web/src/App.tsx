import { Navigate, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider, PublicOnly, RequireAuth } from './auth/AuthContext'
import { AppShell } from './components/AppShell'
import { AgentPage } from './pages/AgentPage'
import { DevicePage } from './pages/DevicePage'
import { DevicesPage } from './pages/DevicesPage'
import { LoginPage } from './pages/LoginPage'
import { TerminalPage } from './pages/TerminalPage'

function Protected({ children }: { children: ReactNode }) {
  return <RequireAuth><AppShell>{children}</AppShell></RequireAuth>
}

export function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<PublicOnly><LoginPage /></PublicOnly>} />
        <Route path="/devices" element={<Protected><DevicesPage /></Protected>} />
        <Route path="/devices/:deviceId" element={<Protected><DevicePage /></Protected>} />
        <Route path="/devices/:deviceId/terminal" element={<Protected><TerminalPage /></Protected>} />
        <Route path="/devices/:deviceId/agent" element={<Protected><AgentPage /></Protected>} />
        <Route path="/" element={<Navigate to="/devices" replace />} />
        <Route path="*" element={<Navigate to="/devices" replace />} />
      </Routes>
    </AuthProvider>
  )
}
