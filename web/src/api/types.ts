export interface User {
  id: string
  username: string
}

export interface Device {
  id: string
  name: string
  platform: string
  arch: string
  client_version: string
  created_at: string
  paired_at: string | null
  last_seen_at: string | null
  online: boolean
}

export interface ErrorDetail {
  code: string
  message: string
  request_id?: string
}

export interface ErrorEnvelope {
  error: ErrorDetail
}

export type ApprovalMode = 'per_command' | 'full_access'

export interface AgentSession {
  id: string
  device_id: string
  approval_mode: ApprovalMode
  state: string
  provider?: string
  created_at?: string
  updated_at?: string
  archived_at?: string | null
  [key: string]: unknown
}

export interface AgentSessionSummary {
  id: string
  device_id: string
  device_name: string
  approval_mode: ApprovalMode
  state: string
  provider: string
  title: string
  created_at: string
  updated_at: string
  archived_at: string | null
}

export interface AgentSettings {
  default_approval_mode: ApprovalMode
  updated_at: string | null
}

export interface DSHCredentialStatus {
  configured: boolean
  writable: boolean
}

export interface RuntimeProviderModel {
  id: string
  name?: string
  context_window?: number
  max_tokens?: number
}

export interface RuntimeProviderProfile {
  id: string
  display_name: string
  family: string
  active: boolean
  configured: boolean
  custom: boolean
  removable: boolean
  revision: number
  base_url?: string
  api?: string
  models: RuntimeProviderModel[]
  models_overridden: boolean
  credential?: DSHCredentialStatus
}

export interface RuntimeProviderDirectory {
  id: string
  display_name: string
  writable: boolean
  custom_provider_revision: number
  protocols: string[]
  providers: RuntimeProviderProfile[]
}

export interface RuntimeProviderMutation {
  expected_revision: number
  display_name?: string
  base_url?: string
  api?: string
  models?: RuntimeProviderModel[]
  models_overridden: boolean
  api_key?: string
}

export interface ModelReasoningEffort {
  id: string
  name: string
  description?: string
}

export interface RuntimeModel {
  id: string
  name: string
  description?: string
  context_window?: number
  max_tokens?: number
  reasoning_efforts: ModelReasoningEffort[]
  default_reasoning_effort?: string
}

export interface ModelProviderGroup {
  id: string
  name: string
  models: RuntimeModel[]
}

export interface ModelSelection {
  provider: string
  model: string
  reasoning_effort?: string
}

export interface ModelDirectory {
  current: ModelSelection
  routable: boolean
  groups: ModelProviderGroup[]
  failures: Array<{ id: string; name: string; message: string }>
  current_credential?: DSHCredentialStatus
}

export interface AgentMessage {
  id: string
  role: 'user' | 'assistant' | 'reasoning'
  content: string
  created_at: string
}

export interface AgentToolCall {
  id: string
  name: string
  arguments_json: string
  status: string
  decision: ToolDecision | null
  exit_code: number | null
  output_excerpt: string | null
  created_at: string
  completed_at: string | null
}

export interface AgentSnapshot {
  session: AgentSession
  messages: AgentMessage[]
  tool_calls: AgentToolCall[]
}

export type ToolDecision = 'approve_once' | 'approve_session' | 'deny'

export const agentEventNames = [
  'session.state',
  'response.reasoning.delta',
  'response.reasoning.done',
  'response.text.delta',
  'response.text.done',
  'tool_call.pending',
  'tool_call.started',
  'tool_call.output',
  'tool_call.completed',
  'turn.completed',
  'turn.failed',
] as const

export type AgentEventName = (typeof agentEventNames)[number]

export interface AgentEventEnvelope {
  event_id?: string
  session_id?: string
  created_at?: string
  type?: string
  payload?: unknown
  [key: string]: unknown
}
