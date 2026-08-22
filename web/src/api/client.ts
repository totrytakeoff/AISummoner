import type {
  AgentSession,
  AgentSnapshot,
  ApprovalMode,
  Device,
  ErrorDetail,
  ErrorEnvelope,
  ToolDecision,
  User,
} from './types'

let unauthorizedHandler: (() => void) | undefined

export class APIError extends Error {
  readonly status: number
  readonly code: string
  readonly requestID?: string

  constructor(status: number, detail: ErrorDetail) {
    super(detail.message)
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
    message: status >= 500 ? 'The server could not complete the request.' : 'The request could not be completed.',
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

  async createAgentSession(deviceID: string, approvalMode: ApprovalMode): Promise<AgentSession> {
    const response = await request<{ session: AgentSession }>(
      `/api/v1/devices/${resourceID(deviceID)}/agent-sessions`,
      { method: 'POST', body: jsonBody({ approval_mode: approvalMode }) },
    )
    return response.session
  },

  latestAgentSession(deviceID: string): Promise<AgentSnapshot> {
    return request<AgentSnapshot>(`/api/v1/devices/${resourceID(deviceID)}/agent-sessions`)
  },

  agentSession(sessionID: string): Promise<AgentSnapshot> {
    return request<AgentSnapshot>(`/api/v1/agent-sessions/${resourceID(sessionID)}`)
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
