import type { ToolCallView } from './events'

export interface AgentProviderPresentation {
  id: string
  label: string
  runtime: 'model' | 'test' | 'unknown'
  workingLabel: string
  emptyTitle: string
  emptyDescription: string
}

export interface AgentToolPresentation {
  kind: 'command' | 'generic'
  title: string
  summary: string
}

const providers: Record<string, AgentProviderPresentation> = {
  dsh: {
    id: 'dsh',
    label: 'DSH',
    runtime: 'model',
    workingLabel: 'DSH 正在处理…',
    emptyTitle: '想让我做什么？',
    emptyDescription: '描述你想要的结果。DSH 只能通过经过授权的远程工具操作这台设备。',
  },
  deepseek: {
    id: 'deepseek',
    label: 'DeepSeek',
    runtime: 'model',
    workingLabel: 'DeepSeek 正在处理…',
    emptyTitle: '想让我做什么？',
    emptyDescription: '描述你想要的结果。DeepSeek 只能通过经过授权的远程工具操作这台设备。',
  },
  opencode: {
    id: 'opencode',
    label: 'OpenCode',
    runtime: 'model',
    workingLabel: 'OpenCode 正在处理…',
    emptyTitle: '想让我做什么？',
    emptyDescription: '描述你想要的结果。Agent 可通过经过授权的远程工具操作这台设备。',
  },
  fake: {
    id: 'fake',
    label: '测试适配器',
    runtime: 'test',
    workingLabel: '正在运行确定性测试步骤…',
    emptyTitle: '确定性 Agent 测试会话',
    emptyDescription: '该适配器不理解自然语言，只用于验证远程命令链路。',
  },
}

const unknownProvider: AgentProviderPresentation = {
  id: 'unknown',
  label: 'Agent',
  runtime: 'unknown',
  workingLabel: 'Agent 正在处理…',
  emptyTitle: '想让我做什么？',
  emptyDescription: '描述你想要的结果。运行时支持的专属能力会在可用时显示。',
}

export function resolveAgentProviderPresentation(provider: string | undefined): AgentProviderPresentation {
  if (!provider) return unknownProvider
  return providers[provider] ?? { ...unknownProvider, id: provider, label: provider }
}

export function presentAgentTool(tool: ToolCallView): AgentToolPresentation {
  if (tool.name === 'remote_exec') {
    return { kind: 'command', title: '运行命令', summary: tool.command }
  }
  return {
    kind: 'generic',
    title: tool.name || '工具调用',
    summary: tool.command === '(command unavailable)' ? '等待工具详情' : tool.command,
  }
}
