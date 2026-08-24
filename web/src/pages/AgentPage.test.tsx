import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'
import type { AgentEventName } from '../api/types'
import type { AgentSnapshot } from '../api/types'
import { AgentPage } from './AgentPage'
import { emptyResponse, jsonResponse, offlineDevice, onlineDevice, renderWithRouter } from '../test/helpers'

class FakeEventSource extends EventTarget {
  static instances: FakeEventSource[] = []
  close = vi.fn()

  constructor(public url: string) {
    super()
    FakeEventSource.instances.push(this)
  }

  open() {
    this.dispatchEvent(new Event('open'))
  }

  emit(type: AgentEventName, sessionID: string, payload: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent(type, {
      data: JSON.stringify({ event_id: `evt_${type}`, session_id: sessionID, payload }),
    }))
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function renderAgent(path = '/devices/dev_online/agent', extra?: ReactNode) {
  return renderWithRouter(
    <>
      {extra}
      <Routes><Route path="/devices/:deviceId/agent" element={<AgentPage />} /></Routes>
    </>,
    path,
  )
}

function noLatestSession() {
  return jsonResponse({ error: { code: 'NOT_FOUND', message: 'resource not found' } }, 404)
}

async function latestEventSource() {
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0))
  return FakeEventSource.instances.at(-1)!
}

function agentSnapshot(overrides: Partial<AgentSnapshot> = {}): AgentSnapshot {
  return {
    session: {
      id: 'ags_latest', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'opencode', state: 'idle',
      created_at: '2026-08-21T10:00:00Z', updated_at: '2026-08-21T10:00:01Z',
    },
    messages: [],
    tool_calls: [],
    ...overrides,
  }
}

describe('Agent page lifecycle and approval boundaries', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => vi.unstubAllGlobals())

  it('automatically creates a command-confirmed conversation when no prior session exists', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_default', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'deepseek', state: 'ready',
      } }))
    renderAgent()

    expect(await screen.findByText('DeepSeek', { selector: '.session-chip.provider' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /DeepSeek/ })).toBeInTheDocument()
    expect(screen.queryByText('DSH 体验层')).not.toBeInTheDocument()
    expect(screen.getByText('逐条确认命令')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start Agent session' })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    await latestEventSource()
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/devices/dev_online/agent-sessions', expect.objectContaining({
      method: 'POST', body: JSON.stringify({}),
    }))
  })

  it('waits without a mode prompt or session mutation while the device is offline', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: offlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
    renderAgent('/devices/dev_offline/agent')

    expect(await screen.findByRole('alert')).toHaveTextContent('重新连接后将自动创建会话')
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start Agent session' })).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('rechecks the latest conversation after automatic creation fails instead of blindly creating twice', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
      .mockResolvedValueOnce(jsonResponse({
        error: { code: 'SERVICE_UNAVAILABLE', message: 'Agent conversation could not start yet.' },
      }, 503))
      .mockResolvedValueOnce(jsonResponse(agentSnapshot({ session: {
        id: 'ags_recovered', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'deepseek', state: 'idle',
      } })))
    renderAgent()

    expect(await screen.findByRole('alert')).toHaveTextContent('服务暂时不可用，请稍后重试。')
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByLabelText('向 Agent 发送消息')).toBeInTheDocument()

    const sessionRequests = fetchMock.mock.calls.filter(([url]) => url === '/api/v1/devices/dev_online/agent-sessions')
    expect(sessionRequests.map(([, init]) => init?.method ?? 'GET')).toEqual(['GET', 'POST', 'GET'])
  })

  it('waits for SSE readiness and preserves completion when events beat a delayed message response', async () => {
    const user = userEvent.setup()
    const postResponse = deferred<Response>()
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_race', device_id: onlineDevice.id, approval_mode: 'per_command', state: 'ready',
      } }))
      .mockImplementationOnce(() => postResponse.promise)
    renderAgent()

    const prompt = await screen.findByLabelText('向 Agent 发送消息')
    const send = screen.getByRole('button', { name: '发送' })
    expect(prompt).toBeDisabled()
    expect(send).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('正在连接 Agent 事件流')

    const source = await latestEventSource()
    act(() => source.open())
    expect(prompt).toBeEnabled()
    await user.type(prompt, 'Inspect this host')
    await user.click(send)
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/agent-sessions/ags_race/messages', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ content: 'Inspect this host' }),
    }))
    expect(screen.getByText('Inspect this host')).toBeInTheDocument()

    act(() => {
      source.emit('response.text.delta', 'ags_race', { delta: 'Inspection complete.' })
      source.emit('response.text.done', 'ags_race', { text: 'Inspection complete.' })
      source.emit('turn.completed', 'ags_race', {})
    })
    expect(screen.getByText('Inspection complete.')).toBeInTheDocument()

    await act(async () => postResponse.resolve(emptyResponse()))
    await waitFor(() => expect(prompt).toBeEnabled())
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
    const messages = Array.from(document.querySelectorAll<HTMLElement>('.chat-message'))
    expect(messages.map((message) => message.textContent)).toEqual([
      expect.stringContaining('Inspect this host'),
      expect.stringContaining('Inspection complete.'),
    ])
  })

  it('labels the Fake provider honestly and puts approval in the composer after the ordered tool row', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_fake', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'fake', state: 'ready',
      } }))
      .mockResolvedValueOnce(emptyResponse())
    renderAgent()

    expect(await screen.findByText('测试适配器', { selector: '.session-chip.provider' })).toBeInTheDocument()
    expect(screen.getByText(/不理解自然语言任务/)).toBeInTheDocument()

    const source = await latestEventSource()
    act(() => {
      source.open()
      source.emit('response.text.delta', 'ags_fake', { delta: 'I will inspect it.' })
      source.emit('tool_call.pending', 'ags_fake', {
        tool_call_id: 'tool_fake', name: 'remote_exec', arguments: { command: 'hostname' },
      })
    })

    const conversation = screen.getByLabelText('Agent 对话')
    expect(conversation.textContent?.indexOf('I will inspect it.')).toBeLessThan(conversation.textContent?.indexOf('hostname') ?? -1)
    expect(screen.getByLabelText('命令审批')).toHaveTextContent('hostname')
    expect(screen.queryByLabelText('向 Agent 发送消息')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '允许当前会话' }))
    expect(screen.getByRole('alertdialog', { name: '允许当前会话后续执行命令？' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(3)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '允许当前会话' }))
    expect(await screen.findByText('完全访问 · 仅当前会话')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/tool-calls/tool_fake/decision', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ decision: 'approve_session' }),
    }))
  })

  it('rolls back an optimistic message and restores the prompt when the server rejects it', async () => {
    const user = userEvent.setup()
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_reject', device_id: onlineDevice.id, approval_mode: 'per_command', state: 'ready',
      } }))
      .mockResolvedValueOnce(jsonResponse({
        error: { code: 'TURN_IN_PROGRESS', message: 'another turn is already running' },
      }, 409))
    renderAgent()

    const prompt = await screen.findByLabelText('向 Agent 发送消息')
    const source = await latestEventSource()
    act(() => source.open())
    await user.type(prompt, 'Do not lose this prompt')
    await user.click(screen.getByRole('button', { name: '发送' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('当前对话正在处理中，请稍候。')
    expect(prompt).toHaveValue('Do not lose this prompt')
    expect(prompt).toBeEnabled()
    expect(document.querySelector('.chat-message.user')).not.toBeInTheDocument()
  })

  it('restores each device latest session without showing the approval picker again', async () => {
    const user = userEvent.setup()
    const deviceA = { ...onlineDevice, id: 'dev_a', name: 'device-a' }
    const deviceB = { ...onlineDevice, id: 'dev_b', name: 'device-b' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_a') return Promise.resolve(jsonResponse({ device: deviceA }))
      if (url === '/api/v1/devices/dev_b') return Promise.resolve(jsonResponse({ device: deviceB }))
      if (url === '/api/v1/devices/dev_a/agent-sessions' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse(agentSnapshot({ session: {
          id: 'ags_a', device_id: 'dev_a', approval_mode: 'full_access', provider: 'opencode', state: 'idle',
        } })))
      }
      if (url === '/api/v1/devices/dev_b/agent-sessions' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse(agentSnapshot({ session: {
          id: 'ags_b', device_id: 'dev_b', approval_mode: 'per_command', provider: 'opencode', state: 'idle',
        } })))
      }
      if (url === '/api/v1/devices/dev_b/agent-sessions' && init?.method === 'POST') {
        return Promise.resolve(jsonResponse({ session: {
          id: 'ags_b_new', device_id: 'dev_b', approval_mode: 'per_command', provider: 'opencode', state: 'idle',
        } }))
      }
      return Promise.reject(new Error(`unexpected fetch ${url} ${init?.method ?? 'GET'}`))
    })
    renderAgent('/devices/dev_a/agent', <Link to="/devices/dev_b/agent">Switch to device B</Link>)

    expect(await screen.findByText('完全访问 · 仅当前会话')).toBeInTheDocument()
    const sourceA = await latestEventSource()

    await user.click(screen.getByRole('link', { name: 'Switch to device B' }))
    expect(await screen.findByRole('link', { name: '← device-b' })).toBeInTheDocument()
    expect(screen.queryByText('完全访问 · 仅当前会话')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/Confirm every command/)).not.toBeInTheDocument()
    expect(sourceA.close).toHaveBeenCalledOnce()

    await user.click(screen.getByRole('button', { name: '新建会话' }))
    await waitFor(() => {
      const createB = fetchMock.mock.calls.find(([url, init]) => url === '/api/v1/devices/dev_b/agent-sessions' && init?.method === 'POST')
      expect(createB?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({}) })
    })
    expect(screen.getByText('逐条确认命令')).toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  })

  it('rehydrates reasoning, tools, and answer as separate rows in the latest conversation', async () => {
    const user = userEvent.setup()
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(jsonResponse(agentSnapshot({
        session: {
          id: 'ags_history', device_id: onlineDevice.id, approval_mode: 'full_access', provider: 'opencode', state: 'idle',
        },
        messages: [
          { id: 'msg_user', role: 'user', content: 'Inspect it', created_at: '2026-08-21T10:00:00Z' },
          { id: 'msg_thought', role: 'reasoning', content: 'I should inspect hostname.', created_at: '2026-08-21T10:00:01Z' },
          { id: 'msg_answer', role: 'assistant', content: 'The host is ready.', created_at: '2026-08-21T10:00:03Z' },
        ],
        tool_calls: [{
          id: 'tool_history', name: 'remote_exec', arguments_json: '{"command":"hostname"}', status: 'completed',
          decision: null, exit_code: 0, output_excerpt: 'lzr-host\n', created_at: '2026-08-21T10:00:02Z', completed_at: '2026-08-21T10:00:02Z',
        }],
      })))
    renderAgent()

    expect(await screen.findByText('The host is ready.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start Agent session' })).not.toBeInTheDocument()
    expect(screen.getByText('hostname')).toBeInTheDocument()
    const reasoning = screen.getByText('思考过程').closest('details')
    expect(reasoning).not.toHaveAttribute('open')
    await user.click(screen.getByText('思考过程'))
    expect(reasoning).toHaveAttribute('open')
    expect(screen.getByText('I should inspect hostname.')).toBeInTheDocument()
  })

  it('shows missing DSH credentials directly and resumes the same failed conversation after configuration', async () => {
    const user = userEvent.setup()
    let configured = false
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return Promise.resolve(jsonResponse({ device: onlineDevice }))
      if (url === '/api/v1/devices/dev_online/agent-sessions' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse(agentSnapshot({ session: {
          id: 'ags_missing_key', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'dsh', state: 'failed',
        } })))
      }
      if (url === '/api/v1/agent-sessions/ags_missing_key/models' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse({
          current: { provider: 'deepseek-official', model: 'deepseek-chat' }, routable: true,
          current_credential: { configured, writable: true }, failures: [],
          groups: [{ id: 'deepseek-official', name: 'DeepSeek', models: [{
            id: 'deepseek-chat', name: 'DeepSeek Chat', reasoning_efforts: [],
          }] }],
        }))
      }
      if (url === '/api/v1/agent-sessions/ags_missing_key/messages' && init?.method === 'POST') {
        return Promise.resolve(jsonResponse({ message: { id: 'msg_retry' } }, 202))
      }
      return Promise.reject(new Error(`unexpected fetch ${url} ${init?.method ?? 'GET'}`))
    })
    renderAgent()

    expect(await screen.findByText('DeepSeek缺少 API 密钥')).toBeInTheDocument()
    expect(screen.queryByText('上一轮 Agent 执行失败')).not.toBeInTheDocument()
    const prompt = screen.getByLabelText('向 Agent 发送消息')
    const source = await latestEventSource()
    act(() => source.open())
    expect(prompt).toBeDisabled()

    configured = true
    act(() => window.dispatchEvent(new Event('aisummoner:dsh-provider-changed')))
    await waitFor(() => expect(prompt).toBeEnabled())
    expect(screen.queryByText('上一轮 Agent 执行失败')).not.toBeInTheDocument()
    await user.type(prompt, '继续原来的任务')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/agent-sessions/ags_missing_key/messages',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ content: '继续原来的任务' }) }),
    ))
    expect(fetchMock.mock.calls.filter(([url, init]) =>
      url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST')).toHaveLength(0)
  })

  it('switches the DSH provider, model, and reasoning effort on the existing conversation', async () => {
    const user = userEvent.setup()
    let selected = { provider: 'deepseek-official', model: 'deepseek-chat', reasoning_effort: '' }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url === '/api/v1/devices/dev_online') return Promise.resolve(jsonResponse({ device: onlineDevice }))
      if (url === '/api/v1/devices/dev_online/agent-sessions' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse(agentSnapshot({ session: {
          id: 'ags_model_picker', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'dsh', state: 'idle',
        } })))
      }
      if (url === '/api/v1/agent-sessions/ags_model_picker/models' && (init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(jsonResponse({
          current: selected, routable: true, current_credential: { configured: true, writable: true }, failures: [],
          groups: [
            { id: 'deepseek-official', name: 'DeepSeek', models: [{
              id: 'deepseek-chat', name: 'DeepSeek Chat', reasoning_efforts: [],
            }] },
            { id: 'acme-gateway', name: 'Acme Gateway', models: [{
              id: 'qwen-coder', name: 'Qwen Coder', default_reasoning_effort: 'medium',
              reasoning_efforts: [{ id: 'medium', name: '中' }, { id: 'high', name: '高' }],
            }] },
          ],
        }))
      }
      if (url === '/api/v1/agent-sessions/ags_model_picker/models' && init?.method === 'PATCH') {
        selected = JSON.parse(String(init.body)) as typeof selected
        return Promise.resolve(jsonResponse({ selected }))
      }
      return Promise.reject(new Error(`unexpected fetch ${url} ${init?.method ?? 'GET'}`))
    })
    renderAgent()

    const picker = await screen.findByRole('button', { name: /DeepSeek Chat/ })
    await user.click(picker)
    await user.click(screen.getByRole('menuitemradio', { name: /Qwen Coder/ }))
    await waitFor(() => expect(screen.getByRole('button', { name: /Qwen Coder/ })).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-sessions/ags_model_picker/models', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ provider: 'acme-gateway', model: 'qwen-coder', reasoning_effort: 'medium' }),
    }))

    await user.click(screen.getByRole('button', { name: /Qwen Coder/ }))
    await user.click(screen.getByRole('button', { name: '高' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-sessions/ags_model_picker/models', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ provider: 'acme-gateway', model: 'qwen-coder', reasoning_effort: 'high' }),
    })))
    expect(selected.reasoning_effort).toBe('high')
  })

  it('edits the authoritative current-session permission with a Full Access risk confirmation', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(jsonResponse(agentSnapshot({ session: {
        id: 'ags_permission', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'opencode', state: 'idle',
      } })))
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_permission', device_id: onlineDevice.id, approval_mode: 'full_access', provider: 'opencode', state: 'idle',
      } }))
    renderAgent()

    const permission = await screen.findByRole('button', { name: '执行命令前询问' })
    await user.click(permission)
    await user.click(screen.getByRole('menuitemradio', { name: /完全访问/ }))
    expect(screen.getByRole('alertdialog', { name: '允许本会话完全访问被控设备？' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '启用完全访问' }))

    expect(await screen.findByRole('button', { name: '完全访问' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/agent-sessions/ags_permission', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ approval_mode: 'full_access' }),
    }))
  })
})
