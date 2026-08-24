import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { APIError, api, setUnauthorizedHandler } from '../api/client'
import type { User } from '../api/types'

type AuthStatus = 'loading' | 'authenticated' | 'anonymous'

interface AuthContextValue {
  status: AuthStatus
  user: User | null
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [user, setUser] = useState<User | null>(null)

  const becomeAnonymous = useCallback(() => {
    setUser(null)
    setStatus('anonymous')
  }, [])

  useEffect(() => {
    setUnauthorizedHandler(becomeAnonymous)
    let active = true
    void api.me().then(
      (currentUser) => {
        if (!active) return
        setUser(currentUser)
        setStatus('authenticated')
      },
      (error: unknown) => {
        if (!active) return
        if (!(error instanceof APIError) || error.status !== 401) {
          // Bootstrap failures leave a retryable login surface instead of a blank app.
        }
        becomeAnonymous()
      },
    )
    return () => {
      active = false
      setUnauthorizedHandler(undefined)
    }
  }, [becomeAnonymous])

  const login = useCallback(async (username: string, password: string) => {
    const nextUser = await api.login(username, password)
    setUser(nextUser)
    setStatus('authenticated')
  }, [])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      becomeAnonymous()
    }
  }, [becomeAnonymous])

  const value = useMemo(() => ({ status, user, login, logout }), [status, user, login, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside AuthProvider')
  return value
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const auth = useAuth()
  const location = useLocation()
  if (auth.status === 'loading') return <main className="centered-state">正在检查登录状态…</main>
  if (auth.status === 'anonymous') {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <>{children}</>
}

export function PublicOnly({ children }: { children: ReactNode }) {
  const auth = useAuth()
  if (auth.status === 'loading') return <main className="centered-state">正在检查登录状态…</main>
  if (auth.status === 'authenticated') return <Navigate to="/devices" replace />
  return <>{children}</>
}
