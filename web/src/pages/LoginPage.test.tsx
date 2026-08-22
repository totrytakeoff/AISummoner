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

    expect(await screen.findByRole('heading', { name: 'Welcome back' })).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Password'), 'correct-password')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('heading', { name: 'Devices' })).toBeInTheDocument()
    const loginCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/auth/login')
    expect(loginCall?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({ username: 'admin', password: 'correct-password' }) })
  })

  it('shows a safe server error and keeps the password out of persistent storage', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'UNAUTHENTICATED', message: 'authentication required' } }, 401))
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'INVALID_CREDENTIALS', message: 'invalid username or password' } }, 401))
    const localStorageSpy = vi.spyOn(Storage.prototype, 'setItem')
    renderApp('/login')

    await userEvent.type(await screen.findByLabelText('Password'), 'never-store-me')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid username or password')
    await waitFor(() => expect(localStorageSpy).not.toHaveBeenCalled())
  })
})
