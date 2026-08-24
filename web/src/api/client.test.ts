import { APIError, api, parseAPIError, setUnauthorizedHandler } from './client'
import { jsonResponse } from '../test/helpers'

describe('API client', () => {
  it('parses the standard error envelope', () => {
    const error = parseAPIError(410, {
      error: { code: 'PAIRING_CODE_EXPIRED', message: 'pairing code has expired', request_id: 'req_1' },
    })
    expect(error).toBeInstanceOf(APIError)
    expect(error).toMatchObject({ status: 410, code: 'PAIRING_CODE_EXPIRED', requestID: 'req_1' })
    expect(error.message).toBe('配对码已过期，请在被控客户端刷新后重试。')
  })

  it('calls the global unauthorized handler on 401', async () => {
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(jsonResponse({
      error: { code: 'UNAUTHENTICATED', message: 'authentication required', request_id: 'req_2' },
    }, 401))

    await expect(api.devices()).rejects.toMatchObject({ status: 401, code: 'UNAUTHENTICATED' })
    expect(unauthorized).toHaveBeenCalledOnce()
    setUnauthorizedHandler(undefined)
  })

  it('loads the fixed recent Agent Session index for one encoded Device', async () => {
    const sessions = [{
      id: 'ags_recent', device_id: 'dev/encoded', approval_mode: 'per_command', provider: 'deepseek',
      state: 'idle', title: 'Inspect remote host', created_at: '2026-08-23T10:00:00Z', updated_at: '2026-08-23T10:01:00Z',
    }]
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(jsonResponse({ sessions }))

    await expect(api.agentSessions('dev/encoded')).resolves.toEqual(sessions)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/devices/dev%2Fencoded/agent-sessions?view=index', expect.objectContaining({
      credentials: 'same-origin',
    }))
  })

  it('writes a DSH credential through the same-origin Server endpoint without retaining it', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(null, { status: 204 }))

    await api.configureDSH('sk-private-dsh-test')

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/agent-provider/dsh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ api_key: 'sk-private-dsh-test' }),
    }))
  })

  it('reads value-free DSH readiness and persists current/default Session permissions separately', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ credential: { configured: false, writable: true } }))
      .mockResolvedValueOnce(jsonResponse({ settings: { default_approval_mode: 'per_command', updated_at: null } }))
      .mockResolvedValueOnce(jsonResponse({ settings: { default_approval_mode: 'full_access', updated_at: '2026-08-24T10:00:00Z' } }))
      .mockResolvedValueOnce(jsonResponse({ session: {
        id: 'ags_one', device_id: 'dev_one', approval_mode: 'full_access', provider: 'dsh', state: 'idle',
      } }))

    await expect(api.dshCredentialStatus()).resolves.toEqual({ configured: false, writable: true })
    await expect(api.agentSettings()).resolves.toMatchObject({ default_approval_mode: 'per_command' })
    await expect(api.updateAgentSettings('full_access')).resolves.toMatchObject({ default_approval_mode: 'full_access' })
    await expect(api.updateAgentSessionApproval('ags_one', 'full_access')).resolves.toMatchObject({ id: 'ags_one', approval_mode: 'full_access' })

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? 'GET', init?.body])).toEqual([
      ['/api/v1/agent-provider/dsh', 'GET', undefined],
      ['/api/v1/agent-settings', 'GET', undefined],
      ['/api/v1/agent-settings', 'PATCH', JSON.stringify({ default_approval_mode: 'full_access' })],
      ['/api/v1/agent-sessions/ags_one', 'PATCH', JSON.stringify({ approval_mode: 'full_access' })],
    ])
  })

  it('uses generic redacted Runtime provider and per-Session model endpoints', async () => {
    const runtime = {
      id: 'dsh', display_name: 'DeepSeek Harness', writable: true, custom_provider_revision: 8,
      protocols: ['openai-completions'], providers: [],
    }
    const models = {
      current: { provider: 'acme-gateway', model: 'qwen-coder', reasoning_effort: 'medium' },
      routable: true, groups: [], failures: [], current_credential: { configured: true, writable: true },
    }
    const mutation = {
      expected_revision: 8, display_name: 'Acme', base_url: 'https://gateway.example/v1',
      api: 'openai-completions', models_overridden: true,
      models: [{ id: 'qwen-coder', context_window: 65536 }], api_key: 'sk-write-only',
    }
    const selection = { provider: 'acme-gateway', model: 'qwen-coder', reasoning_effort: 'high' }
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ runtime }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse(models))
      .mockResolvedValueOnce(jsonResponse({ selected: selection }))

    await expect(api.runtimeProviders('dsh')).resolves.toEqual(runtime)
    await api.configureRuntimeProvider('dsh', 'acme-gateway', mutation)
    await api.removeRuntimeProvider('dsh', 'acme-gateway', 9)
    await expect(api.agentSessionModels('ags_models')).resolves.toEqual(models)
    await expect(api.selectAgentSessionModel('ags_models', selection)).resolves.toEqual(selection)

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? 'GET', init?.body])).toEqual([
      ['/api/v1/agent-runtimes/dsh/providers', 'GET', undefined],
      ['/api/v1/agent-runtimes/dsh/providers/acme-gateway', 'PUT', JSON.stringify(mutation)],
      ['/api/v1/agent-runtimes/dsh/providers/acme-gateway', 'DELETE', JSON.stringify({ expected_revision: 9 })],
      ['/api/v1/agent-sessions/ags_models/models', 'GET', undefined],
      ['/api/v1/agent-sessions/ags_models/models', 'PATCH', JSON.stringify(selection)],
    ])
  })

  it('archives, restores and deletes a Session through bounded management endpoints', async () => {
    const archived = [{
      id: 'ags_archived', device_id: 'dev_one', device_name: 'Remote', approval_mode: 'per_command', provider: 'dsh',
      state: 'failed', title: 'Inspect', created_at: '2026-08-24T09:00:00Z', updated_at: '2026-08-24T10:00:00Z',
      archived_at: '2026-08-24T10:00:00Z',
    }]
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ sessions: archived }))
      .mockResolvedValueOnce(jsonResponse({ session: { ...archived[0], archived_at: null } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))

    await expect(api.archivedAgentSessions()).resolves.toEqual(archived)
    await api.setAgentSessionArchived('ags_archived', false)
    await api.deleteAgentSession('ags_archived')
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/agent-sessions/ags_archived', expect.objectContaining({
      method: 'PATCH', body: JSON.stringify({ archived: false }),
    }))
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/agent-sessions/ags_archived', expect.objectContaining({ method: 'DELETE' }))
  })
})
