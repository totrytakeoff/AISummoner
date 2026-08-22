import { presentAgentTool, resolveAgentProviderPresentation } from './adapters'

describe('Agent presentation adapters', () => {
  it('distinguishes a real model runtime from the deterministic test adapter', () => {
    expect(resolveAgentProviderPresentation('deepseek')).toMatchObject({ label: 'DeepSeek', runtime: 'model' })
    expect(resolveAgentProviderPresentation('opencode')).toMatchObject({ label: 'OpenCode', runtime: 'model' })
    expect(resolveAgentProviderPresentation('fake')).toMatchObject({ label: 'Test adapter', runtime: 'test' })
    expect(resolveAgentProviderPresentation('future-provider')).toMatchObject({
      id: 'future-provider', label: 'future-provider', runtime: 'unknown',
    })
  })

  it('routes known and unknown tools through safe presentation fallbacks', () => {
    expect(presentAgentTool({
      id: 'tool_1', name: 'remote_exec', command: 'hostname', status: 'running', output: '',
    })).toEqual({ kind: 'command', title: 'Run command', summary: 'hostname' })
    expect(presentAgentTool({
      id: 'tool_2', name: 'provider_specific', command: '(command unavailable)', status: 'running', output: '',
    })).toEqual({ kind: 'generic', title: 'provider_specific', summary: 'Waiting for tool details' })
  })
})
