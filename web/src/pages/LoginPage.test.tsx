import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, renderApp } from '../test/helpers'

describe('login flow', () => {
  it('signs in and navigates to devices', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'UNAUTHENTICATED', message: 'authentication required' } }, 401))
      .mockResolvedValueOnce(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      .mockResolvedValueOnce(jsonResponse({ devices: [] }))
    renderApp('/login')

    expect(await screen.findByRole('heading', { name: '欢迎回来' })).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('密码'), 'correct-password')
    await userEvent.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('heading', { name: '设备' })).toBeInTheDocument()
    const loginCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/auth/login')
    expect(loginCall?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({ username: 'admin', password: 'correct-password' }) })
  })

  it('shows a safe server error and keeps the password out of persistent storage', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'UNAUTHENTICATED', message: 'authentication required' } }, 401))
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'INVALID_CREDENTIALS', message: 'invalid username or password' } }, 401))
    const localStorageSpy = vi.spyOn(Storage.prototype, 'setItem')
    renderApp('/login')

    await userEvent.type(await screen.findByLabelText('密码'), 'never-store-me')
    await userEvent.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或密码错误。')
    await waitFor(() => expect(localStorageSpy).not.toHaveBeenCalled())
  })
})
