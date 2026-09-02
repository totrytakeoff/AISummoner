import type { AgentEventEnvelope, AgentEventName, AgentSnapshot, AgentToolCall, ToolDecision } from '../api/types'

export interface AgentMessageView {
  id: string
  role: 'user' | 'assistant'
  content: string
}

export interface ToolCallView {
  id: string
  name: string
  command: string
  cwd?: string
  timeoutMs?: number
  status: string
  decision?: ToolDecision
  output: string
  exitCode?: number
  truncated?: boolean
  denied?: boolean
  failureCode?: string
  error?: string
}

export interface AgentReasoningView {
  id: string
  content: string
  running: boolean
}

export type AgentTimelineItem =
  | { kind: 'message'; key: string; message: AgentMessageView }
  | { kind: 'reasoning'; key: string; reasoning: AgentReasoningView }
  | { kind: 'tool'; key: string; tool: ToolCallView }

export interface AgentViewState {
  sessionState: string
  turnState: 'idle' | 'running' | 'waiting' | 'completed' | 'failed'
  timeline: AgentTimelineItem[]
  failure?: string
  failureCode?: string
}

export interface ParsedAgentEvent {
  type: AgentEventName
  eventID?: string
  sessionID?: string
  createdAt?: string
  payload: Record<string, unknown>
  raw: AgentEventEnvelope
}

export const initialAgentViewState: AgentViewState = {
  sessionState: 'ready',
  turnState: 'idle',
  timeline: [],
}

const turnFailureMessages: Record<string, string> = {
  COMMAND_DENIED: '命令已被拒绝。',
  DEVICE_OFFLINE: '设备当前离线。',
  REMOTE_EXEC_CANCELED: '远程命令已取消。',
  REMOTE_EXEC_TIMEOUT: '远程命令执行超时。',
  REMOTE_EXEC_TRANSPORT: '远程命令通道异常。',
  REMOTE_CWD_INVALID: '远程工作目录无效，请使用被控设备对应平台的绝对路径。',
  REMOTE_POWERSHELL_FAILURE: 'Windows PowerShell 未能启动远程命令。',
  APPROVAL_TIMEOUT: '命令审批已超时。',
  credential_required: '尚未配置 DeepSeek API 密钥，请先在设置中完成配置。',
}

function objectValue(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {}
}

function stringValue(source: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    if (typeof source[key] === 'string') return source[key] as string
  }
  return undefined
}

function numberValue(source: Record<string, unknown>, ...keys: string[]): number | undefined {
  for (const key of keys) {
    if (typeof source[key] === 'number' && Number.isFinite(source[key])) return source[key] as number
  }
  return undefined
}

function booleanValue(source: Record<string, unknown>, ...keys: string[]): boolean | undefined {
  for (const key of keys) {
    if (typeof source[key] === 'boolean') return source[key] as boolean
  }
  return undefined
}

function joinOutputStreams(stdout: string | undefined, stderr: string | undefined): string | undefined {
  if (stdout === undefined && stderr === undefined) return undefined
  if (!stdout) return stderr ?? ''
  if (!stderr) return stdout
  return `${stdout}${stdout.endsWith('\n') ? '' : '\n'}${stderr}`
}

function toolOutput(payload: Record<string, unknown>, ...explicitKeys: string[]): string | undefined {
  const explicit = stringValue(payload, ...explicitKeys)
  if (explicit !== undefined) return explicit
  return joinOutputStreams(stringValue(payload, 'stdout'), stringValue(payload, 'stderr'))
}

function payloadFromEnvelope(envelope: AgentEventEnvelope): Record<string, unknown> {
  const payload = objectValue(envelope.payload)
  if (Object.keys(payload).length > 0) return payload
  const data = objectValue(envelope.data)
  if (Object.keys(data).length > 0) return data
  const copy = { ...envelope }
  delete copy.event_id
  delete copy.session_id
  delete copy.created_at
  delete copy.type
  delete copy.payload
  delete copy.data
  return copy
}

export function parseAgentEvent(type: AgentEventName, data: string): ParsedAgentEvent | null {
  try {
    const parsed = JSON.parse(data) as unknown
    const raw = objectValue(parsed) as AgentEventEnvelope
    return {
      type,
      eventID: typeof raw.event_id === 'string' ? raw.event_id : undefined,
      sessionID: typeof raw.session_id === 'string' ? raw.session_id : undefined,
      createdAt: typeof raw.created_at === 'string' ? raw.created_at : undefined,
      payload: payloadFromEnvelope(raw),
      raw,
    }
  } catch {
    return null
  }
}

function toolID(payload: Record<string, unknown>): string | undefined {
  return stringValue(payload, 'tool_call_id', 'id', 'call_id')
}

function toolArguments(payload: Record<string, unknown>): Record<string, unknown> {
  const value = payload.arguments ?? payload.arguments_json ?? payload.input
  if (typeof value === 'string') {
    try {
      return objectValue(JSON.parse(value))
    } catch {
      return { command: value }
    }
  }
  return objectValue(value)
}

function findTool(timeline: AgentTimelineItem[], id: string): ToolCallView | undefined {
  for (const item of timeline) {
    if (item.kind === 'tool' && item.tool.id === id) return item.tool
  }
  return undefined
}

function upsertTool(timeline: AgentTimelineItem[], id: string, patch: Partial<ToolCallView>): AgentTimelineItem[] {
  const index = timeline.findIndex((item) => item.kind === 'tool' && item.tool.id === id)
  if (index === -1) {
    const tool: ToolCallView = {
      id,
      name: patch.name ?? 'remote_exec',
      command: patch.command ?? '(command unavailable)',
      status: patch.status ?? 'pending',
      output: patch.output ?? '',
      ...patch,
    }
    return [...timeline, { kind: 'tool', key: `tool:${id}`, tool }]
  }
  return timeline.map((item, position) => position === index && item.kind === 'tool'
    ? { ...item, tool: { ...item.tool, ...patch } }
    : item)
}

function messageCount(timeline: AgentTimelineItem[], role: AgentMessageView['role']): number {
  return timeline.filter((item) => item.kind === 'message' && item.message.role === role).length
}

function appendAssistantDelta(timeline: AgentTimelineItem[], text: string): AgentTimelineItem[] {
  if (!text) return timeline
  const last = timeline.at(-1)
  if (last?.kind === 'message' && last.message.role === 'assistant') {
    return timeline.map((item, index) => index === timeline.length - 1 && item.kind === 'message'
      ? { ...item, message: { ...item.message, content: item.message.content + text } }
      : item)
  }
  const id = `assistant-${messageCount(timeline, 'assistant') + 1}`
  return [...timeline, { kind: 'message', key: `message:${id}`, message: { id, role: 'assistant', content: text } }]
}

function reasoningCount(timeline: AgentTimelineItem[]): number {
  return timeline.filter((item) => item.kind === 'reasoning').length
}

function appendReasoningDelta(timeline: AgentTimelineItem[], text: string): AgentTimelineItem[] {
  if (!text) return timeline
  const last = timeline.at(-1)
  if (last?.kind === 'reasoning' && last.reasoning.running) {
    return timeline.map((item, index) => index === timeline.length - 1 && item.kind === 'reasoning'
      ? { ...item, reasoning: { ...item.reasoning, content: item.reasoning.content + text } }
      : item)
  }
  const id = `reasoning-${reasoningCount(timeline) + 1}`
  return [...timeline, {
    kind: 'reasoning', key: `reasoning:${id}`, reasoning: { id, content: text, running: true },
  }]
}

function settleLatestReasoning(timeline: AgentTimelineItem[], text?: string): AgentTimelineItem[] {
  let target = -1
  for (let index = timeline.length - 1; index >= 0; index--) {
    if (timeline[index]?.kind === 'reasoning') {
      target = index
      break
    }
  }
  if (target === -1) {
    return text ? appendReasoningDelta(timeline, text).map((item) => item.kind === 'reasoning'
      ? { ...item, reasoning: { ...item.reasoning, running: false } }
      : item) : timeline
  }
  return timeline.map((item, index) => {
    if (index !== target || item.kind !== 'reasoning') return item
    const content = text && text.startsWith(item.reasoning.content) ? text : item.reasoning.content
    return { ...item, reasoning: { ...item.reasoning, content, running: false } }
  })
}

function settleAllReasoning(timeline: AgentTimelineItem[]): AgentTimelineItem[] {
  return timeline.map((item) => item.kind === 'reasoning'
    ? { ...item, reasoning: { ...item.reasoning, running: false } }
    : item)
}

export function projectAgentEvent(state: AgentViewState, event: ParsedAgentEvent): AgentViewState {
  const payload = event.payload
  switch (event.type) {
    case 'session.state':
      return { ...state, sessionState: stringValue(payload, 'state', 'status') ?? state.sessionState }
    case 'response.reasoning.delta': {
      const text = stringValue(payload, 'delta', 'text', 'content') ?? ''
      return {
        ...state,
        turnState: 'running',
        timeline: appendReasoningDelta(state.timeline, text),
        failure: undefined,
        failureCode: undefined,
      }
    }
    case 'response.reasoning.done':
      return { ...state, timeline: settleLatestReasoning(state.timeline, stringValue(payload, 'text', 'content')) }
    case 'response.text.delta': {
      const text = stringValue(payload, 'delta', 'text', 'content') ?? ''
      return {
        ...state,
        turnState: 'running',
        timeline: appendAssistantDelta(state.timeline, text),
        failure: undefined,
        failureCode: undefined,
      }
    }
    case 'response.text.done': {
      const text = stringValue(payload, 'text', 'content')
      const last = state.timeline.at(-1)
      let timeline = state.timeline
      if (text && last?.kind === 'message' && last.message.role === 'assistant' && text.startsWith(last.message.content)) {
        timeline = state.timeline.map((item, index) => index === state.timeline.length - 1 && item.kind === 'message'
          ? { ...item, message: { ...item.message, content: text } }
          : item)
      } else if (text && (last?.kind !== 'message' || last.message.role !== 'assistant')) {
        timeline = appendAssistantDelta(state.timeline, text)
      }
      return { ...state, timeline }
    }
    case 'tool_call.pending': {
      const id = toolID(payload)
      if (!id) return state
      const args = toolArguments(payload)
      return {
        ...state,
        turnState: 'waiting',
        timeline: upsertTool(state.timeline, id, {
          name: stringValue(payload, 'name', 'tool_name') ?? 'remote_exec',
          command: stringValue(args, 'command') ?? '(command unavailable)',
          cwd: stringValue(args, 'cwd'),
          timeoutMs: numberValue(args, 'timeout_ms'),
          status: 'pending',
        }),
      }
    }
    case 'tool_call.started': {
      const id = toolID(payload)
      if (!id) return state
      const args = toolArguments(payload)
      const patch: Partial<ToolCallView> = { status: 'running' }
      const name = stringValue(payload, 'name', 'tool_name')
      const command = stringValue(args, 'command')
      const cwd = stringValue(args, 'cwd')
      const timeoutMs = numberValue(args, 'timeout_ms')
      if (name !== undefined) patch.name = name
      if (command !== undefined) patch.command = command
      if (cwd !== undefined) patch.cwd = cwd
      if (timeoutMs !== undefined) patch.timeoutMs = timeoutMs
      return { ...state, turnState: 'running', timeline: upsertTool(state.timeline, id, patch) }
    }
    case 'tool_call.output': {
      const id = toolID(payload)
      if (!id) return state
      const chunk = toolOutput(payload, 'output', 'delta', 'text', 'content') ?? ''
      const existing = findTool(state.timeline, id)
      return { ...state, timeline: upsertTool(state.timeline, id, { output: (existing?.output ?? '') + chunk }) }
    }
    case 'tool_call.completed': {
      const id = toolID(payload)
      if (!id) return state
      const existing = findTool(state.timeline, id)
      const completedOutput = toolOutput(payload, 'output', 'output_excerpt')
      const failure = objectValue(payload.failure)
      const denied = booleanValue(payload, 'denied') ?? existing?.denied
      const failureCode = stringValue(failure, 'code')
      return {
        ...state,
        turnState: 'running',
        timeline: upsertTool(state.timeline, id, {
          status: stringValue(payload, 'status') ?? (denied ? 'denied' : failureCode ? 'failed' : 'completed'),
          exitCode: numberValue(payload, 'exit_code', 'exitCode'),
          truncated: booleanValue(payload, 'truncated') ?? existing?.truncated,
          denied,
          failureCode,
          error: stringValue(payload, 'error', 'message') ?? stringValue(failure, 'message'),
          output: completedOutput ?? existing?.output ?? '',
        }),
      }
    }
    case 'turn.completed':
      return { ...state, turnState: 'completed', timeline: settleAllReasoning(state.timeline), failure: undefined, failureCode: undefined }
    case 'turn.failed':
      {
        const failureCode = stringValue(payload, 'code')
        const message = failureCode ? turnFailureMessages[failureCode] : undefined
      return {
        ...state,
        turnState: 'failed',
        timeline: settleAllReasoning(state.timeline),
        failure: message ?? 'Agent 本轮执行失败。',
        failureCode,
      }
      }
  }
}

export function addUserMessage(state: AgentViewState, content: string, messageID?: string): AgentViewState {
  const id = messageID ?? `user-${messageCount(state.timeline, 'user') + 1}`
  return {
    ...state,
    turnState: 'running',
    failure: undefined,
    failureCode: undefined,
    timeline: [...state.timeline, { kind: 'message', key: `message:${id}`, message: { id, role: 'user', content } }],
  }
}

export function markToolDecision(state: AgentViewState, toolIDValue: string, decision: ToolDecision): AgentViewState {
  return {
    ...state,
    timeline: upsertTool(state.timeline, toolIDValue, {
      decision,
      status: decision === 'deny' ? 'denied' : 'approved',
    }),
  }
}

export function timelineMessages(state: AgentViewState): AgentMessageView[] {
  return state.timeline.flatMap((item) => item.kind === 'message' ? [item.message] : [])
}

export function timelineTools(state: AgentViewState): ToolCallView[] {
  return state.timeline.flatMap((item) => item.kind === 'tool' ? [item.tool] : [])
}

function snapshotTool(tool: AgentToolCall): ToolCallView {
  const argumentsValue = toolArguments({ arguments_json: tool.arguments_json })
  const excerpt = tool.output_excerpt ?? ''
  const persistedFailureCode = (tool.status === 'failed' || tool.status === 'denied') && turnFailureMessages[excerpt]
    ? excerpt
    : undefined
  return {
    id: tool.id,
    name: tool.name,
    command: stringValue(argumentsValue, 'command') ?? '(command unavailable)',
    cwd: stringValue(argumentsValue, 'cwd'),
    timeoutMs: numberValue(argumentsValue, 'timeout_ms'),
    status: tool.status,
    decision: tool.decision ?? undefined,
    output: persistedFailureCode ? '' : excerpt,
    exitCode: tool.exit_code ?? undefined,
    denied: tool.status === 'denied',
    failureCode: persistedFailureCode,
  }
}

function snapshotTurnState(state: string): AgentViewState['turnState'] {
  switch (state) {
    case 'running':
      return 'running'
    case 'waiting_approval':
      return 'waiting'
    case 'failed':
      return 'failed'
    default:
      return 'idle'
  }
}

export function projectAgentSnapshot(snapshot: AgentSnapshot): AgentViewState {
  const ordered: Array<{ createdAt: string; priority: number; order: number; item: AgentTimelineItem }> = []
  snapshot.messages.forEach((message, order) => {
    if (message.role === 'reasoning') {
      ordered.push({
        createdAt: message.created_at,
        priority: 1,
        order,
        item: {
          kind: 'reasoning',
          key: `reasoning:${message.id}`,
          reasoning: { id: message.id, content: message.content, running: false },
        },
      })
      return
    }
    ordered.push({
      createdAt: message.created_at,
      priority: message.role === 'user' ? 0 : 3,
      order,
      item: {
        kind: 'message',
        key: `message:${message.id}`,
        message: { id: message.id, role: message.role, content: message.content },
      },
    })
  })
  snapshot.tool_calls.forEach((tool, order) => {
    ordered.push({
      createdAt: tool.created_at,
      priority: 2,
      order: snapshot.messages.length + order,
      item: { kind: 'tool', key: `tool:${tool.id}`, tool: snapshotTool(tool) },
    })
  })
  ordered.sort((left, right) => left.createdAt.localeCompare(right.createdAt) ||
    left.priority - right.priority || left.order - right.order)
  const failed = snapshot.session.state === 'failed'
  return {
    sessionState: snapshot.session.state,
    turnState: snapshotTurnState(snapshot.session.state),
    timeline: ordered.map((entry) => entry.item),
    ...(failed ? {
      failure: '上一轮 Agent 执行失败。可在此重试，或新建会话。',
    } : {}),
  }
}
