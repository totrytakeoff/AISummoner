import { act, screen, waitFor } from '@testing-library/react'
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

    expect(await screen.findAllByText('DeepSeek')).toHaveLength(2)
    expect(screen.getByText('Confirm commands')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start Agent session' })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/devices/dev_online/agent-sessions', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ approval_mode: 'per_command' }),
    }))
  })

  it('configures DeepSeek from a transient password form and starts a new bound conversation', async () => {
    const user = userEvent.setup()
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const secret = 'sk-browser-only-secret-sentinel'
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(jsonResponse(agentSnapshot()))
      .mockResolvedValueOnce(emptyResponse())
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_deepseek_new', device_id: onlineDevice.id, approval_mode: 'per_command', provider: 'deepseek', state: 'idle',
      } }))
    renderAgent()

    await user.click(await screen.findByRole('button', { name: 'Set up DeepSeek' }))
    const dialog = screen.getByRole('dialog', { name: 'Set up DeepSeek' })
    const keyInput = screen.getByLabelText('DeepSeek API key')
    expect(keyInput).toHaveAttribute('type', 'password')
    expect(keyInput).toHaveAttribute('autocomplete', 'off')
    await user.type(keyInput, secret)
    await user.click(screen.getByRole('button', { name: 'Use DeepSeek' }))

    await waitFor(() => expect(dialog).not.toBeInTheDocument())
    expect(screen.getByText(/DeepSeek is ready\. This new conversation/)).toBeInTheDocument()
    const configuration = fetchMock.mock.calls.find(([url]) => url === '/api/v1/agent-provider/deepseek')
    expect(configuration?.[1]).toMatchObject({
      method: 'POST', body: JSON.stringify({ api_key: secret, model: 'deepseek-v4-flash' }),
    })
    const created = fetchMock.mock.calls.find(([url, init]) => url === '/api/v1/devices/dev_online/agent-sessions' && init?.method === 'POST')
    expect(created?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({ approval_mode: 'per_command' }) })
    expect(await screen.findAllByText('DeepSeek')).toHaveLength(2)
    expect(document.body).not.toHaveTextContent(secret)
    expect(storageWrite).not.toHaveBeenCalled()
  })

  it('keeps a rejected DeepSeek key masked for retry and clears it when canceled', async () => {
    const user = userEvent.setup()
    const secret = 'sk-retry-secret-sentinel'
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: onlineDevice }))
      .mockResolvedValueOnce(jsonResponse(agentSnapshot()))
      .mockResolvedValueOnce(jsonResponse({ error: { code: 'INVALID_REQUEST', message: 'invalid request' } }, 400))
    renderAgent()

    await user.click(await screen.findByRole('button', { name: 'Set up DeepSeek' }))
    const keyInput = screen.getByLabelText('DeepSeek API key')
    await user.type(keyInput, secret)
    await user.click(screen.getByRole('button', { name: 'Use DeepSeek' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid request')
    expect(keyInput).toHaveValue(secret)
    expect(keyInput).toHaveAttribute('type', 'password')
    expect(fetchMock).toHaveBeenCalledTimes(3)

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog', { name: 'Set up DeepSeek' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Set up DeepSeek' }))
    expect(screen.getByLabelText('DeepSeek API key')).toHaveValue('')
  })

  it('waits without a mode prompt or session mutation while the device is offline', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ device: offlineDevice }))
      .mockResolvedValueOnce(noLatestSession())
    renderAgent('/devices/dev_offline/agent')

    expect(await screen.findByRole('alert')).toHaveTextContent('start automatically when it reconnects')
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

    expect(await screen.findByRole('alert')).toHaveTextContent('could not start yet')
    await user.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByLabelText('Message the Agent')).toBeInTheDocument()

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

    const prompt = await screen.findByLabelText('Message the Agent')
    const send = screen.getByRole('button', { name: 'Send' })
    expect(prompt).toBeDisabled()
    expect(send).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('Connecting Agent event stream')

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
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
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

    expect(await screen.findByText('Test adapter')).toBeInTheDocument()
    expect(screen.getByText(/does not understand natural-language tasks/)).toBeInTheDocument()

    const source = await latestEventSource()
    act(() => {
      source.open()
      source.emit('response.text.delta', 'ags_fake', { delta: 'I will inspect it.' })
      source.emit('tool_call.pending', 'ags_fake', {
        tool_call_id: 'tool_fake', name: 'remote_exec', arguments: { command: 'hostname' },
      })
    })

    const conversation = screen.getByLabelText('Agent conversation')
    expect(conversation.textContent?.indexOf('I will inspect it.')).toBeLessThan(conversation.textContent?.indexOf('hostname') ?? -1)
    expect(screen.getByLabelText('Command approval')).toHaveTextContent('hostname')
    expect(screen.queryByLabelText('Message the Agent')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Approve session' }))
    expect(screen.getByRole('alertdialog', { name: 'Approve commands for this conversation?' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(3)
    await user.click(screen.getByRole('button', { name: 'Approve this conversation' }))
    expect(await screen.findByText('Full Access · this session')).toBeInTheDocument()
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

    const prompt = await screen.findByLabelText('Message the Agent')
    const source = await latestEventSource()
    act(() => source.open())
    await user.type(prompt, 'Do not lose this prompt')
    await user.click(screen.getByRole('button', { name: 'Send' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('another turn is already running')
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

    expect(await screen.findByText('Full Access · this session')).toBeInTheDocument()
    const sourceA = await latestEventSource()

    await user.click(screen.getByRole('link', { name: 'Switch to device B' }))
    expect(await screen.findByRole('link', { name: '← device-b' })).toBeInTheDocument()
    expect(screen.queryByText('Full Access · this session')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/Confirm every command/)).not.toBeInTheDocument()
    expect(sourceA.close).toHaveBeenCalledOnce()

    await user.click(screen.getByRole('button', { name: 'New conversation' }))
    await waitFor(() => {
      const createB = fetchMock.mock.calls.find(([url, init]) => url === '/api/v1/devices/dev_b/agent-sessions' && init?.method === 'POST')
      expect(createB?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({ approval_mode: 'per_command' }) })
    })
    expect(screen.getByText('Confirm commands')).toBeInTheDocument()
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
    const reasoning = screen.getByText('Think').closest('details')
    expect(reasoning).not.toHaveAttribute('open')
    await user.click(screen.getByText('Think'))
    expect(reasoning).toHaveAttribute('open')
    expect(screen.getByText('I should inspect hostname.')).toBeInTheDocument()
  })
})
