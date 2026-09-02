import type {
  AgentSession,
  AgentSessionSummary,
  AgentSettings,
  AgentSnapshot,
  ApprovalMode,
  Device,
  DSHCredentialStatus,
  ErrorDetail,
  ErrorEnvelope,
  ModelDirectory,
  ModelSelection,
  RuntimeProviderDirectory,
  RuntimeProviderMutation,
  ToolDecision,
  User,
} from './types'

let unauthorizedHandler: (() => void) | undefined

const localizedErrors: Record<string, string> = {
  UNAUTHENTICATED: '登录状态已失效，请重新登录。',
  INVALID_CREDENTIALS: '用户名或密码错误。',
  INVALID_REQUEST: '请求内容无效，请检查后重试。',
  METHOD_NOT_ALLOWED: '当前操作不受支持。',
  NOT_FOUND: '内容不存在或你无权访问。',
  DEVICE_NOT_FOUND: '设备不存在或你无权访问。',
  DEVICE_OFFLINE: '设备当前离线。',
  DEVICE_ALREADY_PAIRED: '该设备已被绑定。',
  PAIRING_CODE_INVALID: '配对码无效或已经使用。',
  PAIRING_CODE_EXPIRED: '配对码已过期，请在被控客户端刷新后重试。',
  ORIGIN_FORBIDDEN: '当前访问来源不受允许。',
  RATE_LIMITED: '操作过于频繁，请稍后重试。',
  SERVICE_UNAVAILABLE: '服务暂时不可用，请稍后重试。',
  SERVER_UNAVAILABLE: '服务端暂时不可用，请稍后重试。',
  DATABASE_UNAVAILABLE: '数据服务暂时不可用，请稍后重试。',
  NOT_READY: '服务尚未就绪，请稍后重试。',
  TURN_IN_PROGRESS: '当前对话正在处理中，请稍候。',
  APPROVAL_TIMEOUT: '命令审批已超时。',
  COMMAND_DENIED: '命令已被拒绝。',
  REMOTE_EXEC_CANCELED: '远程命令已取消。',
  REMOTE_EXEC_TIMEOUT: '远程命令执行超时。',
  REMOTE_EXEC_TRANSPORT: '远程命令通道异常。',
  REMOTE_CWD_INVALID: '远程工作目录无效，请使用被控设备对应平台的绝对路径。',
  REMOTE_POWERSHELL_FAILURE: 'Windows PowerShell 未能启动远程命令。',
  PROVIDER_CREDENTIAL_REQUIRED: '当前模型供应商尚未配置所需的 API 密钥，请先在设置中完成配置。',
  CONFIGURATION_CONFLICT: '供应商配置已被其他操作更新，请刷新后重试。',
  MODEL_UNAVAILABLE: '所选模型当前不可用，请重新选择。',
  TERMINAL_LIMIT: '该设备的终端连接数已达上限。',
  WEBSOCKET_UNAVAILABLE: '实时连接暂时不可用。',
  OPERATION_FAILED: '操作未能完成，请重试。',
  PERSISTENCE_FAILURE: '操作未能保存，请重试。',
  INTERNAL: '服务端暂时无法完成请求。',
  INTERNAL_ERROR: '服务端暂时无法完成请求。',
}

function localizedErrorMessage(status: number, detail: ErrorDetail): string {
  const known = localizedErrors[detail.code]
  if (known) return known
  if (status === 401) return localizedErrors.UNAUTHENTICATED
  if (status === 403) return '你无权执行此操作。'
  if (status === 404) return localizedErrors.NOT_FOUND
  if (status === 429) return localizedErrors.RATE_LIMITED
  if (status >= 500) return '服务端暂时无法完成请求。'
  return '请求未能完成，请检查后重试。'
}

export class APIError extends Error {
  readonly status: number
  readonly code: string
  readonly requestID?: string

  constructor(status: number, detail: ErrorDetail) {
    super(localizedErrorMessage(status, detail))
    this.name = 'APIError'
    this.status = status
    this.code = detail.code
    this.requestID = detail.request_id
  }
}

function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== 'object' || value === null || !('error' in value)) return false
  const error = (value as { error?: unknown }).error
  return (
    typeof error === 'object' &&
    error !== null &&
    typeof (error as { code?: unknown }).code === 'string' &&
    typeof (error as { message?: unknown }).message === 'string'
  )
}

export function parseAPIError(status: number, value: unknown): APIError {
  if (isErrorEnvelope(value)) return new APIError(status, value.error)
  return new APIError(status, {
    code: 'HTTP_ERROR',
    message: '',
  })
}

export function setUnauthorizedHandler(handler: (() => void) | undefined): void {
  unauthorizedHandler = handler
}

async function parseResponse(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined
  const contentType = response.headers.get('content-type') ?? ''
  if (!contentType.toLowerCase().includes('application/json')) return undefined
  try {
    return await response.json()
  } catch {
    return undefined
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  })
  const value = await parseResponse(response)
  if (!response.ok) {
    if (response.status === 401) unauthorizedHandler?.()
    throw parseAPIError(response.status, value)
  }
  return value as T
}

function jsonBody(value: unknown): string {
  return JSON.stringify(value)
}

function resourceID(value: string): string {
  return encodeURIComponent(value)
}

export const api = {
  async me(): Promise<User> {
    const response = await request<{ user: User }>('/api/v1/me')
    return response.user
  },

  async login(username: string, password: string): Promise<User> {
    const response = await request<{ user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: jsonBody({ username, password }),
    })
    return response.user
  },

  logout(): Promise<void> {
    return request<void>('/api/v1/auth/logout', { method: 'POST' })
  },

  async devices(): Promise<Device[]> {
    const response = await request<{ devices: Device[] }>('/api/v1/devices')
    return response.devices
  },

  async device(deviceID: string): Promise<Device> {
    const response = await request<{ device: Device }>(`/api/v1/devices/${resourceID(deviceID)}`)
    return response.device
  },

  async claimPairing(code: string): Promise<Device> {
    const response = await request<{ device: Device }>('/api/v1/pairings/claim', {
      method: 'POST',
      body: jsonBody({ code }),
    })
    return response.device
  },

  unpair(deviceID: string): Promise<void> {
    return request<void>(`/api/v1/devices/${resourceID(deviceID)}`, { method: 'DELETE' })
  },

  configureDeepSeek(apiKey: string, model = 'deepseek-v4-flash'): Promise<void> {
    return request<void>('/api/v1/agent-provider/deepseek', {
      method: 'POST',
      body: jsonBody({ api_key: apiKey, model }),
    })
  },

  configureDSH(apiKey: string): Promise<void> {
    return request<void>('/api/v1/agent-provider/dsh', {
      method: 'POST',
      body: jsonBody({ api_key: apiKey }),
    })
  },

  async dshCredentialStatus(): Promise<DSHCredentialStatus> {
    const response = await request<{ credential: DSHCredentialStatus }>('/api/v1/agent-provider/dsh')
    return response.credential
  },

  async runtimeProviders(runtime = 'dsh'): Promise<RuntimeProviderDirectory> {
    const response = await request<{ runtime: RuntimeProviderDirectory }>(
      `/api/v1/agent-runtimes/${resourceID(runtime)}/providers`,
    )
    return response.runtime
  },

  configureRuntimeProvider(runtime: string, provider: string, mutation: RuntimeProviderMutation): Promise<void> {
    return request<void>(
      `/api/v1/agent-runtimes/${resourceID(runtime)}/providers/${resourceID(provider)}`,
      { method: 'PUT', body: jsonBody(mutation) },
    )
  },

  removeRuntimeProvider(runtime: string, provider: string, expectedRevision: number): Promise<void> {
    return request<void>(
      `/api/v1/agent-runtimes/${resourceID(runtime)}/providers/${resourceID(provider)}`,
      { method: 'DELETE', body: jsonBody({ expected_revision: expectedRevision }) },
    )
  },

  async agentSettings(): Promise<AgentSettings> {
    const response = await request<{ settings: AgentSettings }>('/api/v1/agent-settings')
    return response.settings
  },

  async updateAgentSettings(defaultApprovalMode: ApprovalMode): Promise<AgentSettings> {
    const response = await request<{ settings: AgentSettings }>('/api/v1/agent-settings', {
      method: 'PATCH',
      body: jsonBody({ default_approval_mode: defaultApprovalMode }),
    })
    return response.settings
  },

  async createAgentSession(deviceID: string, approvalMode?: ApprovalMode): Promise<AgentSession> {
    const response = await request<{ session: AgentSession }>(
      `/api/v1/devices/${resourceID(deviceID)}/agent-sessions`,
      { method: 'POST', body: jsonBody(approvalMode ? { approval_mode: approvalMode } : {}) },
    )
    return response.session
  },

  latestAgentSession(deviceID: string): Promise<AgentSnapshot> {
    return request<AgentSnapshot>(`/api/v1/devices/${resourceID(deviceID)}/agent-sessions`)
  },

  async agentSessions(deviceID: string): Promise<AgentSessionSummary[]> {
    const response = await request<{ sessions: AgentSessionSummary[] }>(
      `/api/v1/devices/${resourceID(deviceID)}/agent-sessions?view=index`,
    )
    return response.sessions
  },

  async archivedAgentSessions(): Promise<AgentSessionSummary[]> {
    const response = await request<{ sessions: AgentSessionSummary[] }>('/api/v1/agent-sessions?view=archived')
    return response.sessions
  },

  agentSession(sessionID: string): Promise<AgentSnapshot> {
    return request<AgentSnapshot>(`/api/v1/agent-sessions/${resourceID(sessionID)}`)
  },

  agentSessionModels(sessionID: string): Promise<ModelDirectory> {
    return request<ModelDirectory>(`/api/v1/agent-sessions/${resourceID(sessionID)}/models`)
  },

  async selectAgentSessionModel(sessionID: string, selection: ModelSelection): Promise<ModelSelection> {
    const response = await request<{ selected: ModelSelection }>(
      `/api/v1/agent-sessions/${resourceID(sessionID)}/models`,
      { method: 'PATCH', body: jsonBody(selection) },
    )
    return response.selected
  },

  async updateAgentSessionApproval(sessionID: string, approvalMode: ApprovalMode): Promise<AgentSession> {
    const response = await request<{ session: AgentSession }>(`/api/v1/agent-sessions/${resourceID(sessionID)}`, {
      method: 'PATCH',
      body: jsonBody({ approval_mode: approvalMode }),
    })
    return response.session
  },

  async setAgentSessionArchived(sessionID: string, archived: boolean): Promise<AgentSession> {
    const response = await request<{ session: AgentSession }>(`/api/v1/agent-sessions/${resourceID(sessionID)}`, {
      method: 'PATCH',
      body: jsonBody({ archived }),
    })
    return response.session
  },

  deleteAgentSession(sessionID: string): Promise<void> {
    return request<void>(`/api/v1/agent-sessions/${resourceID(sessionID)}`, { method: 'DELETE' })
  },

  postAgentMessage(sessionID: string, content: string): Promise<unknown> {
    return request<unknown>(`/api/v1/agent-sessions/${resourceID(sessionID)}/messages`, {
      method: 'POST',
      body: jsonBody({ content }),
    })
  },

  decideToolCall(toolCallID: string, decision: ToolDecision): Promise<unknown> {
    return request<unknown>(`/api/v1/tool-calls/${resourceID(toolCallID)}/decision`, {
      method: 'POST',
      body: jsonBody({ decision }),
    })
  },
}

export function normalizePairingCode(value: string): string {
  const compact = value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8)
  return compact.length > 4 ? `${compact.slice(0, 4)}-${compact.slice(4)}` : compact
}

export function terminalWebSocketURL(deviceID: string): string {
  const url = new URL(`/api/v1/devices/${resourceID(deviceID)}/terminal`, window.location.href)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

export function agentEventsURL(sessionID: string): string {
  return `/api/v1/agent-sessions/${resourceID(sessionID)}/events`
}
