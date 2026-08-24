import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes, useNavigate } from 'react-router-dom'
import type { AgentSessionSummary } from '../api/types'
import { jsonResponse, onlineDevice, renderWithRouter } from '../test/helpers'
import { WorkspacePage } from './WorkspacePage'

const terminalLifecycle = vi.hoisted(() => ({ mounts: [] as string[], unmounts: [] as string[] }))
const authLifecycle = vi.hoisted(() => ({ logout: vi.fn(async () => {}) }))

vi.mock('../auth/AuthContext', () => ({
  useAuth: () => ({ user: { id: 'usr_1', username: 'admin' }, logout: authLifecycle.logout }),
}))

vi.mock('../terminal/TerminalPanel', async () => {
  const { useEffect } = await vi.importActual<typeof import('react')>('react')
  return {
    TerminalPanel: ({ deviceID }: { deviceID: string }) => {
      useEffect(() => {
        terminalLifecycle.mounts.push(deviceID)
        return () => { terminalLifecycle.unmounts.push(deviceID) }
      }, [deviceID])
      return <div data-testid="terminal-panel">Terminal for {deviceID}</div>
    },
  }
})

class FakeEventSource extends EventTarget {
  static instances: FakeEventSource[] = []
  close = vi.fn()

  constructor(public url: string) {
    super()
    FakeEventSource.instances.push(this)
  }
}

function summary(id: string, title: string, updatedAt: string): AgentSessionSummary {
  return {
    id, title, updated_at: updatedAt, created_at: updatedAt, device_id: onlineDevice.id,
    device_name: onlineDevice.name, archived_at: null,
    approval_mode: 'per_command', provider: 'deepseek', state: 'idle',
  }
}

function snapshot(session: AgentSessionSummary, content: string) {
  return {
    session,
    messages: [{ id: `msg_${session.id}`, role: 'assistant', content, created_at: session.updated_at }],
    tool_calls: [],
  }
}

function renderWorkspace(path = '/devices/dev_online/workspace') {
  return renderWithRouter(
    <WorkspaceRouteHarness />,
    path,
  )
}

function WorkspaceRouteHarness() {
  const navigate = useNavigate()
  return (
    <>
      <button type="button" onClick={() => navigate('/devices/dev_other/workspace')}>Switch device route</button>
      <button type="button" onClick={() => navigate('/devices/dev_online/workspace')}>Switch original route</button>
      <Routes><Route path="/devices/:deviceId/workspace" element={<WorkspacePage />} /></Routes>
    </>
  )
}

describe('Control Workspace', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    terminalLifecycle.mounts = []
    terminalLifecycle.unmounts = []
    authLifecycle.logout.mockClear()
    vi.stubGlobal('EventSource', FakeEventSource)
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })
    const values = new Map<string, string>()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => { values.set(key, value) },
        removeItem: (key: string) => { values.delete(key) },
        clear: () => values.clear(),
        key: (index: number) => Array.from(values.keys())[index] ?? null,
        get length() { return values.size },
      },
    })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('loads the recent index, switches exact Sessions and closes the old SSE', async () => {
    const user = userEvent.setup()
    const first = summary('ags_first', 'First investigation', '2026-08-23T12:00:00Z')
    const second = summary('ags_second', 'Second investigation', '2026-08-22T12:00:00Z')
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions: [first, second] })
      if (url === '/api/v1/agent-sessions/ags_first') return jsonResponse(snapshot(first, 'First answer'))
      if (url === '/api/v1/agent-sessions/ags_second') return jsonResponse(snapshot(second, 'Second answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    expect(await screen.findByText('First answer')).toBeInTheDocument()
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    const firstSource = FakeEventSource.instances[0]!
    expect(firstSource.url).toBe('/api/v1/agent-sessions/ags_first/events')

    await user.click(within(document.querySelector('.workspace-toolbar-actions')!).getByRole('button', { name: '组件' }))
    expect(terminalLifecycle.mounts).toEqual(['dev_online'])
    await user.click(screen.getByRole('button', { name: /^Second investigation,/ }))
    expect(await screen.findByText('Second answer')).toBeInTheDocument()
    await waitFor(() => expect(firstSource.close).toHaveBeenCalledOnce())
    await waitFor(() => expect(terminalLifecycle.unmounts).toEqual(['dev_online']))
    expect(screen.queryByText('First answer')).not.toBeInTheDocument()
    expect(FakeEventSource.instances.at(-1)?.url).toBe('/api/v1/agent-sessions/ags_second/events')
  })

  it('creates a per-command conversation, opens tools and releases Terminal on close', async () => {
    const user = userEvent.setup()
    const initial = summary('ags_initial', 'Initial conversation', '2026-08-23T12:00:00Z')
    const created = summary('ags_created', 'New conversation', '2026-08-23T13:00:00Z')
    let sessions = [initial]
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions })
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') {
        sessions = [created, initial]
        return jsonResponse({ session: created }, 201)
      }
      if (url === '/api/v1/agent-sessions/ags_initial') return jsonResponse(snapshot(initial, 'Initial answer'))
      if (url === '/api/v1/agent-sessions/ags_created') return jsonResponse(snapshot(created, 'Created answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Initial answer')
    await user.click(screen.getByRole('button', { name: '新建会话' }))
    expect(await screen.findByText('Created answer')).toBeInTheDocument()
    const createCall = fetchMock.mock.calls.find(([url, init]) => url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST')
    expect(createCall?.[1]?.body).toBe(JSON.stringify({}))

    const toolbar = within(document.querySelector('.workspace-toolbar-actions')!)
    expect(toolbar.queryByRole('button', { name: '终端' })).not.toBeInTheDocument()
    expect(toolbar.queryByRole('button', { name: '设备' })).not.toBeInTheDocument()
    await user.click(toolbar.getByRole('button', { name: '组件' }))
    expect(screen.getByTestId('terminal-panel')).toHaveTextContent('dev_online')
    const dock = screen.getByRole('complementary', { name: '工作区组件' })
    expect(within(dock).getByRole('tab', { name: '终端' })).toHaveAttribute('aria-selected', 'true')
    await user.click(within(dock).getByRole('tab', { name: '设备' }))
    expect(within(dock).getByText(onlineDevice.id)).toBeInTheDocument()
    await user.click(within(dock).getByRole('tab', { name: '终端' }))
    await user.click(screen.getByRole('button', { name: '最大化组件栏' }))
    expect(dock.parentElement).toHaveAttribute('data-dock-maximized', 'true')
    await user.click(screen.getByRole('button', { name: '关闭组件栏' }))
    expect(screen.queryByTestId('terminal-panel')).not.toBeInTheDocument()
    await waitFor(() => expect(document.activeElement).toBe(toolbar.getByRole('button', { name: '组件' })))
  })

  it('keeps model and Device management in DSH Settings instead of the retired Manage page', async () => {
    const user = userEvent.setup()
    const current = summary('ags_settings', 'Settings conversation', '2026-08-23T12:00:00Z')
    const secret = 'sk-settings-integration-secret'
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online' && init?.method === 'DELETE') return new Response(null, { status: 204 })
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions: [current] })
      if (url === '/api/v1/agent-sessions/ags_settings') return jsonResponse(snapshot(current, 'Settings answer'))
      if (url === '/api/v1/agent-runtimes/dsh/providers' && (init?.method ?? 'GET') === 'GET') {
        return jsonResponse({ runtime: {
          id: 'dsh', display_name: 'DeepSeek Harness', writable: true, custom_provider_revision: 4,
          protocols: ['openai-completions'], providers: [{
            id: 'deepseek-official', display_name: 'DeepSeek', family: 'llm-deepseek', active: true,
            configured: true, custom: false, removable: false, revision: 3, models: [{ id: 'deepseek-chat' }],
            models_overridden: false, credential: { configured: false, writable: true },
          }],
        } })
      }
      if (url === '/api/v1/agent-runtimes/dsh/providers/deepseek-official' && init?.method === 'PUT') {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Settings answer')
    expect(screen.queryByRole('link', { name: '管理' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '设置' }))
    const settings = screen.getByRole('dialog', { name: '设置' })
    await user.click(within(settings).getByRole('button', { name: 'Agent 与模型' }))
    await user.click(await within(settings).findByRole('button', { name: /DeepSeek/ }))
    await user.type(within(settings).getByLabelText('API 密钥'), secret)
    await user.click(within(settings).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-runtimes/dsh/providers/deepseek-official', expect.objectContaining({
      method: 'PUT', body: JSON.stringify({ expected_revision: 3, base_url: '', models_overridden: false, api_key: secret }),
    })))
    expect(document.body).not.toHaveTextContent(secret)

    await user.click(within(settings).getByRole('button', { name: '设备' }))
    expect(within(settings).getByText(onlineDevice.id)).toBeInTheDocument()
    await user.click(within(settings).getByRole('button', { name: '解除绑定' }))
    await user.click(screen.getByRole('button', { name: '确认解除绑定' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/devices/dev_online', expect.objectContaining({ method: 'DELETE' })))
  })

  it('deduplicates automatic first-Session creation on an empty online Device', async () => {
    const user = userEvent.setup()
    const created = summary('ags_first_created', 'New conversation', '2026-08-23T13:00:00Z')
    let sessions: AgentSessionSummary[] = []
    let createCalls = 0
    let resolveCreate!: (response: Response) => void
    const create = new Promise<Response>((resolve) => { resolveCreate = resolve })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions })
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') {
        createCalls++
        return create
      }
      if (url === '/api/v1/agent-sessions/ags_first_created') return jsonResponse(snapshot(created, 'First answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    expect(await screen.findByText('正在新建会话…')).toBeInTheDocument()
    const newConversation = screen.getByRole('button', { name: '新建会话' })
    expect(newConversation).toBeDisabled()
    await user.click(newConversation)
    expect(createCalls).toBe(1)
    sessions = [created]
    await act(async () => resolveCreate(jsonResponse({ session: created }, 201)))
    expect(await screen.findByText('First answer')).toBeInTheDocument()
    expect(createCalls).toBe(1)
  })

  it('keeps first-Session creation admission per Device across a rapid A-B-A route cycle', async () => {
    const user = userEvent.setup()
    const otherDevice = { ...onlineDevice, id: 'dev_other', name: 'other-host' }
    const createdA = summary('ags_created_a', 'Created on A', '2026-08-23T13:00:00Z')
    const createdB = { ...summary('ags_created_b', 'Created on B', '2026-08-23T13:01:00Z'), device_id: otherDevice.id }
    let sessionsA: AgentSessionSummary[] = []
    let sessionsB: AgentSessionSummary[] = []
    let createCallsA = 0
    let createCallsB = 0
    let resolveCreateA!: (response: Response) => void
    let resolveCreateB!: (response: Response) => void
    const createA = new Promise<Response>((resolve) => { resolveCreateA = resolve })
    const createB = new Promise<Response>((resolve) => { resolveCreateB = resolve })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url === '/api/v1/devices/dev_other') return jsonResponse({ device: otherDevice })
      if (url === '/api/v1/devices/dev_online/agent-sessions?view=index') return jsonResponse({ sessions: sessionsA })
      if (url === '/api/v1/devices/dev_other/agent-sessions?view=index') return jsonResponse({ sessions: sessionsB })
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') {
        createCallsA++
        return createA
      }
      if (url === '/api/v1/devices/dev_other/agent-sessions' && init?.method === 'POST') {
        createCallsB++
        return createB
      }
      if (url === '/api/v1/agent-sessions/ags_created_a') return jsonResponse(snapshot(createdA, 'A answer'))
      if (url === '/api/v1/agent-sessions/ags_created_b') return jsonResponse(snapshot(createdB, 'B answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    expect(await screen.findByText('正在新建会话…')).toBeInTheDocument()
    await waitFor(() => expect(createCallsA).toBe(1))
    await user.click(screen.getByRole('button', { name: 'Switch device route' }))
    await waitFor(() => expect(document.querySelector('.workspace-device-identity strong')).toHaveTextContent('other-host'))
    expect(await screen.findByText('正在新建会话…')).toBeInTheDocument()
    await waitFor(() => expect(createCallsB).toBe(1))
    await user.click(screen.getByRole('button', { name: 'Switch original route' }))
    await waitFor(() => expect(document.querySelector('.workspace-device-identity strong')).toHaveTextContent('lzr-host'))
    expect(await screen.findByText('正在新建会话…')).toBeInTheDocument()
    await waitFor(() => {
      expect(createCallsA).toBe(1)
      expect(createCallsB).toBe(1)
    })

    sessionsA = [createdA]
    sessionsB = [createdB]
    await act(async () => {
      resolveCreateB(jsonResponse({ session: createdB }, 201))
      resolveCreateA(jsonResponse({ session: createdA }, 201))
    })
    expect(await screen.findByText('A answer')).toBeInTheDocument()
    expect(createCallsA).toBe(1)
    expect(createCallsB).toBe(1)
  })

  it('keeps existing rows after create failure and provides a successful retry', async () => {
    const user = userEvent.setup()
    const initial = summary('ags_kept', 'Keep this conversation', '2026-08-23T12:00:00Z')
    const created = summary('ags_retry', 'Retry result', '2026-08-23T13:00:00Z')
    let sessions = [initial]
    let createCalls = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions })
      if (url === '/api/v1/agent-sessions/ags_kept') return jsonResponse(snapshot(initial, 'Kept answer'))
      if (url === '/api/v1/agent-sessions/ags_retry') return jsonResponse(snapshot(created, 'Retry answer'))
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') {
        createCalls++
        if (createCalls === 1) return jsonResponse({ error: { code: 'INTERNAL', message: 'Could not create conversation.' } }, 500)
        sessions = [created, initial]
        return jsonResponse({ session: created }, 201)
      }
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Kept answer')
    await user.click(screen.getByRole('button', { name: '新建会话' }))
    expect(await screen.findByText('服务端暂时无法完成请求。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Keep this conversation,/ })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('Retry answer')).toBeInTheDocument()
    expect(createCalls).toBe(2)
  })

  it('keeps the last valid index during refresh errors and retries in place', async () => {
    const callbacks: Array<() => void> = []
    vi.spyOn(window, 'setInterval').mockImplementation(((callback: TimerHandler) => {
      if (typeof callback === 'function') callbacks.push(callback as () => void)
      return callbacks.length
    }) as typeof window.setInterval)
    const current = summary('ags_refresh', 'Refresh-safe conversation', '2026-08-23T12:00:00Z')
    let failRefresh = false
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) {
        if (failRefresh) return jsonResponse({ error: { code: 'INTERNAL', message: 'Could not refresh conversations.' } }, 500)
        return jsonResponse({ sessions: [current] })
      }
      if (url === '/api/v1/agent-sessions/ags_refresh') return jsonResponse(snapshot(current, 'Refresh-safe answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Refresh-safe answer')
    failRefresh = true
    await act(async () => { callbacks.forEach((callback) => callback()) })
    expect(await screen.findByText('服务端暂时无法完成请求。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Refresh-safe conversation,/ })).toBeEnabled()
    failRefresh = false
    await userEvent.click(screen.getByRole('button', { name: '重新加载' }))
    await waitFor(() => expect(screen.queryByText('服务端暂时无法完成请求。')).not.toBeInTheDocument())
    expect(screen.getByText('Refresh-safe answer')).toBeInTheDocument()
  })

  it('restores narrow Session selection/create focus and opens a visible Terminal', async () => {
    const user = userEvent.setup()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
    const current = summary('ags_laptop', 'Laptop session', '2026-08-23T12:00:00Z')
    const created = summary('ags_laptop_created', 'Laptop created session', '2026-08-23T13:00:00Z')
    let sessions = [current]
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions })
      if (url === '/api/v1/agent-sessions/ags_laptop') return jsonResponse(snapshot(current, 'Laptop answer'))
      if (url === '/api/v1/agent-sessions/ags_laptop_created') return jsonResponse(snapshot(created, 'Created laptop answer'))
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') {
        sessions = [created, current]
        return jsonResponse({ session: created }, 201)
      }
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Laptop answer')
    await user.click(screen.getByRole('button', { name: '会话' }))
    await user.click(screen.getByRole('button', { name: /^Laptop session,/ }))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Agent' })))
    await user.click(screen.getByRole('button', { name: '会话' }))
    await user.click(screen.getByRole('button', { name: '新建会话' }))
    expect(await screen.findByText('Created laptop answer')).toBeInTheDocument()
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Agent' })))
    await user.click(within(document.querySelector('.workspace-mobile-nav')!).getByRole('button', { name: '组件' }))
    expect(screen.getByTestId('terminal-panel')).toBeInTheDocument()
    expect(document.querySelector('.workspace-frame')).toHaveAttribute('data-single-panel', 'true')
    expect(document.querySelector('.workspace-dock-column')).not.toHaveAttribute('aria-hidden')
    await user.click(screen.getByRole('button', { name: '关闭组件栏' }))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Agent' })))
  })

  it('ignores a late snapshot from the previously selected Session', async () => {
    const user = userEvent.setup()
    const first = summary('ags_slow', 'Slow conversation', '2026-08-23T12:00:00Z')
    const second = summary('ags_fast', 'Fast conversation', '2026-08-22T12:00:00Z')
    let resolveSlow!: (response: Response) => void
    const slow = new Promise<Response>((resolve) => { resolveSlow = resolve })
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url.endsWith('/agent-sessions?view=index')) return jsonResponse({ sessions: [first, second] })
      if (url === '/api/v1/agent-sessions/ags_slow') return slow
      if (url === '/api/v1/agent-sessions/ags_fast') return jsonResponse(snapshot(second, 'Fast answer'))
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await user.click(await screen.findByRole('button', { name: /^Fast conversation,/ }))
    expect(await screen.findByText('Fast answer')).toBeInTheDocument()
    await act(async () => resolveSlow(jsonResponse(snapshot(first, 'Stale answer'))))
    expect(screen.queryByText('Stale answer')).not.toBeInTheDocument()
    expect(screen.getByText('Fast answer')).toBeInTheDocument()
  })

  it('ignores a late Session creation after the Device route changes', async () => {
    const user = userEvent.setup()
    const original = summary('ags_original', 'Original device', '2026-08-23T12:00:00Z')
    const otherDevice = { ...onlineDevice, id: 'dev_other', name: 'other-host' }
    const other = { ...summary('ags_other', 'Other device', '2026-08-23T13:00:00Z'), device_id: otherDevice.id }
    const staleCreated = summary('ags_stale_created', 'Stale creation', '2026-08-23T14:00:00Z')
    let resolveCreate!: (response: Response) => void
    const create = new Promise<Response>((resolve) => { resolveCreate = resolve })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return jsonResponse({ device: onlineDevice })
      if (url === '/api/v1/devices/dev_other') return jsonResponse({ device: otherDevice })
      if (url === '/api/v1/devices/dev_online/agent-sessions?view=index') return jsonResponse({ sessions: [original] })
      if (url === '/api/v1/devices/dev_other/agent-sessions?view=index') return jsonResponse({ sessions: [other] })
      if (url === '/api/v1/agent-sessions/ags_original') return jsonResponse(snapshot(original, 'Original answer'))
      if (url === '/api/v1/agent-sessions/ags_other') return jsonResponse(snapshot(other, 'Other answer'))
      if (url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST') return create
      throw new Error(`unexpected request ${url}`)
    })
    renderWorkspace()

    await screen.findByText('Original answer')
    await user.click(within(document.querySelector('.workspace-toolbar-actions')!).getByRole('button', { name: '组件' }))
    expect(terminalLifecycle.mounts).toEqual(['dev_online'])
    await user.click(screen.getByRole('button', { name: '新建会话' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/devices/dev_online/agent-sessions', expect.objectContaining({ method: 'POST' }),
    ))
    await user.click(screen.getByRole('button', { name: 'Switch device route' }))
    expect(await screen.findByText('Other answer')).toBeInTheDocument()
    await waitFor(() => expect(terminalLifecycle.unmounts).toEqual(['dev_online']))
    await act(async () => resolveCreate(jsonResponse({ session: staleCreated }, 201)))
    expect(screen.getByText('Other answer')).toBeInTheDocument()
    expect(screen.queryByText('Stale creation')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalledWith('/api/v1/agent-sessions/ags_stale_created', expect.anything())
  })
})
