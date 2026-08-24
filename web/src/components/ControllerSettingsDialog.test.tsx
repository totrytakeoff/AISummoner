import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ControllerSettingsDialog } from './ControllerSettingsDialog'
import { jsonResponse, onlineDevice } from '../test/helpers'
import type { AgentSessionSummary } from '../api/types'

const defaults = {
  device: onlineDevice,
  runtimeLabel: 'OpenCode',
  username: 'admin',
  onClose: vi.fn(),
  onUnpair: vi.fn(async () => {}),
  onSignOut: vi.fn(async () => {}),
}

function runtimeDirectory(configured: boolean) {
  return {
    runtime: {
      id: 'dsh', display_name: 'DeepSeek Harness', writable: true, custom_provider_revision: 4,
      protocols: ['openai-completions', 'openai-responses', 'anthropic-messages'],
      providers: [{
        id: 'deepseek-official', display_name: 'DeepSeek', family: 'llm-deepseek', active: true,
        configured: true, custom: false, removable: false, revision: 3, models: [{ id: 'deepseek-chat' }],
        models_overridden: false, credential: { configured, writable: true },
      }],
    },
  }
}

function installSettingsFetch(
  configured: () => boolean = () => false,
  archived: AgentSessionSummary[] = [],
  mutateProvider: (body: string | undefined) => Promise<Response> = async () => new Response(null, { status: 204 }),
) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    if (url === '/api/v1/agent-runtimes/dsh/providers' && method === 'GET') {
      return Promise.resolve(jsonResponse(runtimeDirectory(configured())))
    }
    if (url.startsWith('/api/v1/agent-runtimes/dsh/providers/') && method === 'PUT') {
      return mutateProvider(init?.body?.toString())
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
    const fetchMock = installSettingsFetch()
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const secret = 'sk-controller-settings-secret'
    render(<ControllerSettingsDialog {...defaults} initialSection="agent" />)

    expect(document.querySelector('.settings-hero-card p')).toHaveTextContent('DSH 体验层 · OpenCode 运行时')
    expect(screen.getByText('一等运行时 · 已接入')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: /DeepSeek/ }))
    const key = screen.getByLabelText('API 密钥')
    expect(key).toHaveAttribute('type', 'password')
    expect(key).toHaveAttribute('autocomplete', 'off')
    await user.type(key, secret)
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/agent-runtimes/dsh/providers/deepseek-official',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({
          expected_revision: 3, base_url: '', models_overridden: false, api_key: secret,
        }),
      }),
    ))
    expect(screen.queryByLabelText('API 密钥')).not.toBeInTheDocument()
    expect(screen.getByText(/当前会话无需重建/)).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent(secret)
    expect(storageWrite).not.toHaveBeenCalled()
  })

  it('retains a rejected key only while the modal is open and clears it after close', async () => {
    const user = userEvent.setup()
    const secret = 'sk-retry-only-in-memory'
    installSettingsFetch(() => false, [], async () => jsonResponse({
      error: { code: 'INVALID_REQUEST', message: 'invalid request', request_id: 'req_provider' },
    }, 400))

    function Harness() {
      const [open, setOpen] = useState(true)
      return open ? (
        <ControllerSettingsDialog
          {...defaults}
          initialSection="agent"
          onClose={() => setOpen(false)}
        />
      ) : <button type="button" onClick={() => setOpen(true)}>重新打开设置</button>
    }

    render(<Harness />)
    await user.click(await screen.findByRole('button', { name: /DeepSeek/ }))
    const key = screen.getByLabelText('API 密钥')
    await user.type(key, secret)
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('请求内容无效，请检查后重试。')).toBeInTheDocument()
    expect(key).toHaveValue(secret)

    await user.click(screen.getAllByRole('button', { name: '关闭设置' }).at(-1)!)
    await user.click(screen.getByRole('button', { name: '重新打开设置' }))
    await user.click(await screen.findByRole('button', { name: /DeepSeek/ }))
    expect(screen.getByLabelText('API 密钥')).toHaveValue('')
  })

  it('creates a custom DSH provider with its protocol and bounded model metadata', async () => {
    const user = userEvent.setup()
    const fetchMock = installSettingsFetch()
    render(<ControllerSettingsDialog {...defaults} initialSection="agent" />)

    await user.click(await screen.findByRole('button', { name: '添加自定义供应商' }))
    await user.type(screen.getByLabelText('供应商 ID'), 'my-gateway')
    await user.type(screen.getByLabelText('显示名称'), '我的网关')
    await user.type(screen.getByLabelText('API 地址'), 'https://gateway.example/v1')
    await user.selectOptions(screen.getByLabelText('接口协议'), 'openai-responses')
    await user.type(screen.getByLabelText('模型 1 ID'), 'qwen-coder')
    await user.type(screen.getByLabelText('模型 1 名称'), 'Qwen Coder')
    await user.type(screen.getByLabelText('模型 1 上下文'), '65536')
    await user.type(screen.getByLabelText('模型 1 最大输出'), '8192')
    await user.click(screen.getByRole('button', { name: '创建供应商' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/agent-runtimes/dsh/providers/my-gateway',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({
          expected_revision: 4, display_name: '我的网关', base_url: 'https://gateway.example/v1',
          api: 'openai-responses', models_overridden: true,
          models: [{ id: 'qwen-coder', name: 'Qwen Coder', context_window: 65536, max_tokens: 8192 }],
        }),
      }),
    ))
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
