import { useState } from 'react'
import type { FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { APIError } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { InlineError } from '../components/InlineError'
import { AISummonerMark } from '../components/Icons'

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
      setError(nextError instanceof APIError ? nextError.message : '无法登录，请重试。')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card" aria-labelledby="login-title">
        <div className="brand auth-brand">
          <span className="brand-mark" aria-hidden="true"><AISummonerMark /></span>
          <span>AISummoner</span>
        </div>
        <h1 id="login-title">欢迎回来</h1>
        <p className="muted">登录后即可连接设备并继续 Agent 会话。</p>
        <form onSubmit={submit} className="stack-form">
          <label htmlFor="username">用户名</label>
          <input
            id="username"
            name="username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            required
          />
          <label htmlFor="password">密码</label>
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
            {submitting ? '正在登录…' : '登录'}
          </button>
        </form>
      </section>
    </main>
  )
}
