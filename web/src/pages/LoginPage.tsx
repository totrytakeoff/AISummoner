import { useState } from 'react'
import type { FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { APIError } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { InlineError } from '../components/InlineError'

interface ReturnLocationState {
  from?: string
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSubmitting(true)
    setError(null)
    try {
      await login(username.trim(), password)
      const state = location.state as ReturnLocationState | null
      navigate(state?.from || '/devices', { replace: true })
    } catch (nextError) {
      setError(nextError instanceof APIError ? nextError.message : 'Unable to sign in. Try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card" aria-labelledby="login-title">
        <div className="brand auth-brand">
          <span className="brand-mark" aria-hidden="true">A</span>
          <span>AISummoner</span>
        </div>
        <p className="eyebrow">Remote execution control plane</p>
        <h1 id="login-title">Welcome back</h1>
        <p className="muted">Sign in to connect a terminal or Agent to your devices.</p>
        <form onSubmit={submit} className="stack-form">
          <label htmlFor="username">Username</label>
          <input
            id="username"
            name="username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            required
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            required
          />
          <InlineError message={error} />
          <button className="button primary" type="submit" disabled={submitting}>
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </section>
    </main>
  )
}
