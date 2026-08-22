import { initialAgentViewState, parseAgentEvent, projectAgentEvent, projectAgentSnapshot, timelineMessages, timelineTools } from './events'

function event(type: Parameters<typeof parseAgentEvent>[0], payload: Record<string, unknown>) {
  const parsed = parseAgentEvent(type, JSON.stringify({ event_id: 'evt_1', session_id: 'ags_1', payload }))
  if (!parsed) throw new Error('fixture did not parse')
  return parsed
}

describe('Agent event projection', () => {
  it('increments assistant text and projects a complete tool lifecycle', () => {
    let state = projectAgentEvent(initialAgentViewState, event('response.text.delta', { delta: 'Checking ' }))
    state = projectAgentEvent(state, event('response.text.delta', { delta: 'now.' }))
    state = projectAgentEvent(state, event('tool_call.pending', {
      tool_call_id: 'tool_1',
      name: 'remote_exec',
      arguments: { command: 'uname -a', cwd: '/home/myself', timeout_ms: 30000 },
    }))
    state = projectAgentEvent(state, event('tool_call.started', { tool_call_id: 'tool_1' }))
    state = projectAgentEvent(state, event('tool_call.output', { tool_call_id: 'tool_1', output: 'Linux ' }))
    state = projectAgentEvent(state, event('tool_call.output', { tool_call_id: 'tool_1', output: 'host' }))
    state = projectAgentEvent(state, event('tool_call.completed', { tool_call_id: 'tool_1', exit_code: 0 }))
    state = projectAgentEvent(state, event('turn.completed', {}))

    expect(timelineMessages(state)).toEqual([{ id: 'assistant-1', role: 'assistant', content: 'Checking now.' }])
    expect(timelineTools(state)[0]).toMatchObject({
      id: 'tool_1', command: 'uname -a', cwd: '/home/myself', timeoutMs: 30000,
      status: 'completed', output: 'Linux host', exitCode: 0,
    })
    expect(state.turnState).toBe('completed')
  })

  it('ignores malformed and unknown payload fields safely', () => {
    expect(parseAgentEvent('turn.failed', '{bad json')).toBeNull()
    const state = projectAgentEvent(initialAgentViewState, event('turn.failed', { reason: 'rate_limited', extra: { future: true } }))
    expect(state).toMatchObject({ turnState: 'failed', failure: 'rate_limited' })
  })

  it('projects the canonical Task006 stdout/stderr, truncation, denial and failure fields', () => {
    let state = projectAgentEvent(initialAgentViewState, event('tool_call.pending', {
      tool_call_id: 'tool_result',
      name: 'remote_exec',
      arguments: { command: 'systemctl status example' },
    }))
    state = projectAgentEvent(state, event('tool_call.completed', {
      tool_call_id: 'tool_result',
      stdout: 'partial stdout',
      stderr: 'partial stderr',
      exit_code: 1,
      truncated: true,
      denied: false,
      failure: { code: 'REMOTE_EXEC_TRANSPORT', message: 'remote execution failed' },
    }))

    expect(timelineTools(state)[0]).toMatchObject({
      status: 'failed',
      output: 'partial stdout\npartial stderr',
      exitCode: 1,
      truncated: true,
      denied: false,
      failureCode: 'REMOTE_EXEC_TRANSPORT',
      error: 'remote execution failed',
    })

    state = projectAgentEvent(state, event('tool_call.completed', {
      tool_call_id: 'tool_result', denied: true, failure: { code: 'COMMAND_DENIED', message: 'command denied' },
    }))
    expect(timelineTools(state)[0]).toMatchObject({ status: 'denied', denied: true, failureCode: 'COMMAND_DENIED' })
  })

  it('projects command metadata when a full-access call starts without a pending event', () => {
    const state = projectAgentEvent(initialAgentViewState, event('tool_call.started', {
      tool_call_id: 'tool_full_access',
      name: 'remote_exec',
      arguments: { command: 'hostname', cwd: '/home/myself', timeout_ms: 30000 },
    }))

    expect(timelineTools(state)).toEqual([expect.objectContaining({
      id: 'tool_full_access',
      name: 'remote_exec',
      command: 'hostname',
      cwd: '/home/myself',
      timeoutMs: 30000,
      status: 'running',
    })])
    expect(state.turnState).toBe('running')
  })

  it('keeps messages and tools in event order and starts a new assistant row after a tool', () => {
    let state = projectAgentEvent(initialAgentViewState, event('response.text.delta', { delta: 'I will inspect it.' }))
    state = projectAgentEvent(state, event('tool_call.started', {
      tool_call_id: 'tool_ordered', name: 'remote_exec', arguments: { command: 'hostname' },
    }))
    state = projectAgentEvent(state, event('tool_call.completed', { tool_call_id: 'tool_ordered', exit_code: 0 }))
    state = projectAgentEvent(state, event('response.text.delta', { delta: 'The host is ready.' }))

    expect(state.timeline.map((item) => item.kind === 'message'
      ? item.message.content
      : item.kind === 'reasoning' ? item.reasoning.content : item.tool.command)).toEqual([
      'I will inspect it.',
      'hostname',
      'The host is ready.',
    ])
    expect(timelineMessages(state)).toHaveLength(2)
  })

  it('keeps reasoning separate from answer text and settles it at turn completion', () => {
    let state = projectAgentEvent(initialAgentViewState, event('response.reasoning.delta', { delta: 'Inspect ' }))
    state = projectAgentEvent(state, event('response.reasoning.delta', { delta: 'the host.' }))
    state = projectAgentEvent(state, event('response.reasoning.done', { text: 'Inspect the host.' }))
    state = projectAgentEvent(state, event('response.text.delta', { delta: 'The host is healthy.' }))
    state = projectAgentEvent(state, event('turn.completed', {}))

    expect(state.timeline).toEqual([
      expect.objectContaining({ kind: 'reasoning', reasoning: expect.objectContaining({ content: 'Inspect the host.', running: false }) }),
      expect.objectContaining({ kind: 'message', message: expect.objectContaining({ role: 'assistant', content: 'The host is healthy.' }) }),
    ])
  })

  it('projects a persisted snapshot into chronological reasoning, tool, and answer rows', () => {
    const state = projectAgentSnapshot({
      session: { id: 'ags_resume', device_id: 'dev_1', approval_mode: 'full_access', provider: 'opencode', state: 'idle' },
      messages: [
        { id: 'msg_user', role: 'user', content: 'Inspect', created_at: '2026-08-21T10:00:00Z' },
        { id: 'msg_reasoning', role: 'reasoning', content: 'Plan', created_at: '2026-08-21T10:00:01Z' },
        { id: 'msg_answer', role: 'assistant', content: 'Done', created_at: '2026-08-21T10:00:03Z' },
      ],
      tool_calls: [{
        id: 'tool_resume', name: 'remote_exec', arguments_json: '{"command":"hostname","timeout_ms":30000}',
        status: 'completed', decision: null, exit_code: 0, output_excerpt: 'lzr-host\n',
        created_at: '2026-08-21T10:00:02Z', completed_at: '2026-08-21T10:00:02Z',
      }],
    })

    expect(state.timeline.map((item) => item.kind)).toEqual(['message', 'reasoning', 'tool', 'message'])
    expect(timelineTools(state)[0]).toMatchObject({ command: 'hostname', timeoutMs: 30000, output: 'lzr-host\n' })
  })
})
