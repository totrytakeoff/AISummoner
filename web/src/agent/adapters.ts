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
  deepseek: {
    id: 'deepseek',
    label: 'DeepSeek',
    runtime: 'model',
    workingLabel: 'DeepSeek is working…',
    emptyTitle: 'What should I investigate?',
    emptyDescription: 'Describe the result you want. DeepSeek can inspect this device only through approved remote tools.',
  },
  opencode: {
    id: 'opencode',
    label: 'OpenCode',
    runtime: 'model',
    workingLabel: 'OpenCode is working…',
    emptyTitle: 'What should I investigate?',
    emptyDescription: 'Describe the result you want. The Agent can inspect this device through approved remote tools.',
  },
  fake: {
    id: 'fake',
    label: 'Test adapter',
    runtime: 'test',
    workingLabel: 'Running deterministic test steps…',
    emptyTitle: 'Deterministic Agent test session',
    emptyDescription: 'This adapter does not understand prompts. It only verifies the remote command pipeline.',
  },
}

const unknownProvider: AgentProviderPresentation = {
  id: 'unknown',
  label: 'Agent',
  runtime: 'unknown',
  workingLabel: 'Agent is working…',
  emptyTitle: 'What should I investigate?',
  emptyDescription: 'Describe the result you want. Provider-specific capabilities will appear when available.',
}

export function resolveAgentProviderPresentation(provider: string | undefined): AgentProviderPresentation {
  if (!provider) return unknownProvider
  return providers[provider] ?? { ...unknownProvider, id: provider, label: provider }
}

export function presentAgentTool(tool: ToolCallView): AgentToolPresentation {
  if (tool.name === 'remote_exec') {
    return { kind: 'command', title: 'Run command', summary: tool.command }
  }
  return {
    kind: 'generic',
    title: tool.name || 'Tool call',
    summary: tool.command === '(command unavailable)' ? 'Waiting for tool details' : tool.command,
  }
}
