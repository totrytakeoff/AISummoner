import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, offlineDevice, onlineDevice, renderApp } from '../test/helpers'

describe('devices page', () => {
  it('shows online and offline states', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      .mockResolvedValueOnce(jsonResponse({ devices: [onlineDevice, offlineDevice] }))
    renderApp('/devices')

    expect(await screen.findByText('lzr-host')).toBeInTheDocument()
    expect(screen.getByText('archive-host')).toBeInTheDocument()
    expect(screen.getByText('在线')).toBeInTheDocument()
    expect(screen.getByText('离线')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /lzr-host/ })).toHaveAttribute('href', '/devices/dev_online/workspace')
  })

  it('normalizes a pairing code and refreshes the list after success', async () => {
    const paired = { ...onlineDevice, id: 'dev_paired', name: 'new-host' }
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      .mockResolvedValueOnce(jsonResponse({ devices: [] }))
      .mockResolvedValueOnce(jsonResponse({ device: paired }))
      .mockResolvedValueOnce(jsonResponse({ devices: [paired] }))
    renderApp('/devices')

    const field = await screen.findByLabelText('配对码')
    await userEvent.type(field, 'k7hf 92pq')
    expect(field).toHaveValue('K7HF-92PQ')
    await userEvent.click(screen.getByRole('button', { name: '绑定设备' }))

    expect(await screen.findByText('new-host')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('已成功绑定 new-host。')
    await waitFor(() => {
      const pairingCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/pairings/claim')
      expect(pairingCall?.[1]?.body).toBe(JSON.stringify({ code: 'K7HF-92PQ' }))
    })
  })

  it('opens global Settings from the Device hub without device-only controls', async () => {
    const user = userEvent.setup()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url === '/api/v1/me') return Promise.resolve(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      if (url === '/api/v1/devices') return Promise.resolve(jsonResponse({ devices: [onlineDevice] }))
      if (url === '/api/v1/agent-provider/dsh') {
        return Promise.resolve(jsonResponse({ credential: { configured: true, writable: true } }))
      }
      if (url === '/api/v1/agent-settings') {
        return Promise.resolve(jsonResponse({ settings: { default_approval_mode: 'per_command', updated_at: null } }))
      }
      return Promise.reject(new Error(`unexpected Device hub fetch ${url}`))
    })
    renderApp('/devices')

    await screen.findByText('lzr-host')
    await user.click(screen.getByRole('button', { name: '打开设置' }))
    expect(screen.getByRole('dialog', { name: '设置' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Agent 与模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '会话管理' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '设备' })).not.toBeInTheDocument()
  })
})
