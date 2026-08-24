import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ControllerSettingsDialog } from './ControllerSettingsDialog'
import { APIError } from '../api/client'
import { jsonResponse, onlineDevice } from '../test/helpers'
import type { AgentSessionSummary } from '../api/types'

const defaults = {
  device: onlineDevice,
  runtimeLabel: 'OpenCode',
  username: 'admin',
  onClose: vi.fn(),
  onConfigureDSH: vi.fn(async () => {}),
  onUnpair: vi.fn(async () => {}),
  onSignOut: vi.fn(async () => {}),
}

function installSettingsFetch(configured: () => boolean = () => false, archived: AgentSessionSummary[] = []) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    if (url === '/api/v1/agent-provider/dsh' && method === 'GET') {
      return Promise.resolve(jsonResponse({ credential: { configured: configured(), writable: true } }))
    }
    if (url === '/api/v1/agent-settings' && method === 'GET') {
      return Promise.resolve(jsonResponse({ settings: { default_approval_mode: 'per_command', updated_at: null } }))
    }
    if (url === '/api/v1/agent-settings' && method === 'PATCH') {
      return Promise.resolve(jsonResponse({ settings: { default_approval_mode: 'full_access', updated_at: '2026-08-24T10:00:00Z' } }))
    }
    if (url === '/api/v1/agent-sessions?view=archived' && method === 'GET') {
      return Promise.resolve(jsonResponse({ sessions: archived }))
    }
    if (url.startsWith('/api/v1/agent-sessions/') && method === 'PATCH') {
      return Promise.resolve(jsonResponse({ session: { ...archived[0], archived_at: null } }))
    }
    if (url.startsWith('/api/v1/agent-sessions/') && method === 'DELETE') {
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    return Promise.reject(new Error(`unexpected settings fetch ${url} ${method}`))
  })
}

describe('DSH-first Controller settings', () => {
  it('separates the DSH experience from the actual runtime and keeps provider secrets transient', async () => {
    const user = userEvent.setup()
    let configured = false
    const configure = vi.fn(async () => { configured = true })
    installSettingsFetch(() => configured)
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const secret = 'sk-controller-settings-secret'
    render(<ControllerSettingsDialog {...defaults} initialSection="agent" onConfigureDSH={configure} />)

    expect(document.querySelector('.settings-hero-card p')).toHaveTextContent('DSH 体验层 · OpenCode 运行时')
    expect(screen.getByText('一等运行时 · 已接入')).toBeInTheDocument()
    const key = screen.getByLabelText('DeepSeek API 密钥')
    expect(key).toHaveAttribute('type', 'password')
    expect(key).toHaveAttribute('autocomplete', 'off')
    await user.type(key, secret)
    await user.click(screen.getByRole('button', { name: '保存密钥' }))

    await waitFor(() => expect(configure).toHaveBeenCalledWith(secret))
    expect(key).toHaveValue('')
    expect(screen.getByText(/当前会话和旧会话都可以立即继续使用/)).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent(secret)
    expect(storageWrite).not.toHaveBeenCalled()
  })

  it('retains a rejected key only while the modal is open and clears it after close', async () => {
    const user = userEvent.setup()
    const secret = 'sk-retry-only-in-memory'
    installSettingsFetch()

    function Harness() {
      const [open, setOpen] = useState(true)
      return open ? (
        <ControllerSettingsDialog
          {...defaults}
          initialSection="agent"
          onClose={() => setOpen(false)}
          onConfigureDSH={async () => { throw new APIError(400, { code: 'INVALID_REQUEST', message: 'invalid request' }) }}
        />
      ) : <button type="button" onClick={() => setOpen(true)}>重新打开设置</button>
    }

    render(<Harness />)
    const key = screen.getByLabelText('DeepSeek API 密钥')
    await user.type(key, secret)
    await user.click(screen.getByRole('button', { name: '保存密钥' }))
    expect(await screen.findByText('请求内容无效，请检查后重试。')).toBeInTheDocument()
    expect(key).toHaveValue(secret)

    await user.click(screen.getAllByRole('button', { name: '关闭设置' }).at(-1)!)
    await user.click(screen.getByRole('button', { name: '重新打开设置' }))
    expect(screen.getByLabelText('DeepSeek API 密钥')).toHaveValue('')
  })

  it('keeps device management inside Settings and requires confirmation before unpair', async () => {
    const user = userEvent.setup()
    const unpair = vi.fn(async () => {})
    installSettingsFetch()
    render(<ControllerSettingsDialog {...defaults} initialSection="device" onUnpair={unpair} />)

    expect(screen.getByRole('dialog', { name: '设置' })).toHaveTextContent(onlineDevice.id)
    await user.click(screen.getByRole('button', { name: '解除绑定' }))
    expect(screen.getByRole('alertdialog', { name: `解除 ${onlineDevice.name} 的绑定？` })).toBeInTheDocument()
    expect(unpair).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '确认解除绑定' }))
    await waitFor(() => expect(unpair).toHaveBeenCalledOnce())
  })

  it('persists a confirmed default permission only for future sessions', async () => {
    const user = userEvent.setup()
    const fetchMock = installSettingsFetch()
    render(<ControllerSettingsDialog {...defaults} initialSection="agent" />)

    await user.click(await screen.findByRole('radio', { name: /完全访问/ }))
    expect(screen.getByRole('alertdialog', { name: '将完全访问设为默认？' })).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: '设为默认' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-settings', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ default_approval_mode: 'full_access' }),
    })))
  })

  it('manages archived sessions globally without exposing device-only controls', async () => {
    const user = userEvent.setup()
    const archived: AgentSessionSummary = {
      id: 'ags_archived', device_id: 'dev_archived', device_name: '远程主机', approval_mode: 'per_command',
      provider: 'dsh', state: 'failed', title: '检查服务', created_at: '2026-08-23T10:00:00Z',
      updated_at: '2026-08-23T10:01:00Z', archived_at: '2026-08-24T10:00:00Z',
    }
    const fetchMock = installSettingsFetch(() => true, [archived])
    render(<ControllerSettingsDialog {...defaults} device={undefined} initialSection="sessions" />)

    expect(screen.queryByRole('button', { name: '设备' })).not.toBeInTheDocument()
    expect(await screen.findByText('检查服务')).toBeInTheDocument()
    expect(screen.getByText(/远程主机/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '恢复会话：检查服务' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-sessions/ags_archived', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ archived: false }),
    })))
    await user.click(screen.getByRole('button', { name: '刷新' }))
    await user.click(await screen.findByRole('button', { name: '删除会话：检查服务' }))
    expect(screen.getByRole('alertdialog', { name: '永久删除“检查服务”？' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '永久删除' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-sessions/ags_archived', expect.objectContaining({
      method: 'DELETE',
    })))
  })

  it('keeps the selected tab when device polling returns a fresh snapshot', async () => {
    const user = userEvent.setup()
    installSettingsFetch()
    const { rerender } = render(<ControllerSettingsDialog {...defaults} initialSection="general" />)

    await user.click(screen.getByRole('button', { name: 'Agent 与模型' }))
    expect(screen.getByRole('heading', { name: 'Agent 与模型' })).toBeInTheDocument()

    rerender(
      <ControllerSettingsDialog
        {...defaults}
        device={{ ...onlineDevice, last_seen_at: '2026-08-24T12:00:05Z' }}
        initialSection="general"
      />,
    )

    expect(screen.getByRole('heading', { name: 'Agent 与模型' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '通用' })).not.toBeInTheDocument()
  })

  it('falls back to General only when the Device settings scope disappears', async () => {
    const user = userEvent.setup()
    installSettingsFetch()
    const { rerender } = render(<ControllerSettingsDialog {...defaults} initialSection="device" />)

    expect(screen.getByRole('heading', { name: '设备' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Agent 与模型' }))
    expect(screen.getByRole('heading', { name: 'Agent 与模型' })).toBeInTheDocument()

    rerender(<ControllerSettingsDialog {...defaults} device={undefined} initialSection="device" />)

    expect(await screen.findByRole('heading', { name: '通用' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '设备' })).not.toBeInTheDocument()
  })
})
