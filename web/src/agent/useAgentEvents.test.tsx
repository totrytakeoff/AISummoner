import { act, render, screen } from '@testing-library/react'
import { agentEventNames } from '../api/types'
import type { AgentEventName } from '../api/types'
import { useAgentEvents } from './useAgentEvents'
import type { AgentViewState } from './events'

class FakeEventSource extends EventTarget {
  static instances: FakeEventSource[] = []
  close = vi.fn()
  added: string[] = []
  removed: string[] = []

  constructor(public url: string) {
    super()
    FakeEventSource.instances.push(this)
  }

  override addEventListener(type: string, callback: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions) {
    this.added.push(type)
    super.addEventListener(type, callback, options)
  }

  override removeEventListener(type: string, callback: EventListenerOrEventListenerObject | null, options?: boolean | EventListenerOptions) {
    this.removed.push(type)
    super.removeEventListener(type, callback, options)
  }

  open() {
    this.dispatchEvent(new Event('open'))
  }

  fail() {
    this.dispatchEvent(new Event('error'))
  }

  emit(type: AgentEventName, sessionID: string, payload: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent(type, {
      data: JSON.stringify({ event_id: `evt_${type}`, session_id: sessionID, payload }),
    }))
  }
}

function Harness({ sessionID, initialView }: { sessionID: string | null; initialView?: AgentViewState }) {
  const { state, streamState } = useAgentEvents(sessionID, initialView)
  return (
    <div>
      <output data-testid="stream-state">{streamState}</output>
      <output data-testid="turn-state">{state.turnState}</output>
      {state.timeline.map((item) => item.kind === 'message'
        ? <p key={item.key}>{item.message.content}</p>
        : item.kind === 'reasoning'
          ? <p key={item.key}>{item.reasoning.content}</p>
          : <p key={item.key}>{item.tool.command}</p>)}
      {state.failure && <p>{state.failure}</p>}
    </div>
  )
}

describe('Agent event stream lifecycle', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => vi.unstubAllGlobals())

  it('tracks readiness, consumes every named event, and resets cleanly when the session changes', () => {
    const view = render(<Harness sessionID="ags_a" />)
    const first = FakeEventSource.instances[0]
    expect(first.url).toBe('/api/v1/agent-sessions/ags_a/events')
    expect(first.added).toEqual(expect.arrayContaining(['open', 'error', ...agentEventNames]))
    expect(screen.getByTestId('stream-state')).toHaveTextContent('connecting')

    act(() => {
      first.open()
      first.emit('response.text.delta', 'ags_a', { delta: 'session A answer' })
      first.emit('tool_call.pending', 'ags_a', {
        tool_call_id: 'tool_a',
        name: 'remote_exec',
        arguments: { command: 'hostname' },
      })
    })
    expect(screen.getByTestId('stream-state')).toHaveTextContent('open')
    expect(screen.getByText('session A answer')).toBeInTheDocument()
    expect(screen.getByText('hostname')).toBeInTheDocument()

    act(() => first.fail())
    expect(screen.getByTestId('stream-state')).toHaveTextContent('error')
    expect(screen.getByText(/event stream disconnected/)).toBeInTheDocument()
    act(() => first.open())
    expect(screen.getByTestId('stream-state')).toHaveTextContent('open')
    expect(screen.queryByText(/event stream disconnected/)).not.toBeInTheDocument()

    view.rerender(<Harness sessionID="ags_b" />)
    const second = FakeEventSource.instances[1]
    expect(first.close).toHaveBeenCalledOnce()
    expect(first.removed).toEqual(expect.arrayContaining(['open', 'error', ...agentEventNames]))
    expect(screen.queryByText('session A answer')).not.toBeInTheDocument()
    expect(screen.queryByText('hostname')).not.toBeInTheDocument()
    expect(screen.getByTestId('stream-state')).toHaveTextContent('connecting')

    act(() => {
      first.emit('response.text.delta', 'ags_a', { delta: 'stale event' })
      second.open()
      second.emit('response.text.delta', 'ags_b', { delta: 'session B answer' })
    })
    expect(screen.queryByText('stale event')).not.toBeInTheDocument()
    expect(screen.getByText('session B answer')).toBeInTheDocument()

    view.unmount()
    expect(second.close).toHaveBeenCalledOnce()
    expect(second.removed).toEqual(expect.arrayContaining(['open', 'error', ...agentEventNames]))
  })

  it('starts from a rehydrated snapshot before consuming new live events', () => {
    const initialView: AgentViewState = {
      sessionState: 'idle',
      turnState: 'idle',
      timeline: [{
        kind: 'message', key: 'message:msg_existing',
        message: { id: 'msg_existing', role: 'assistant', content: 'Existing answer' },
      }],
    }
    render(<Harness sessionID="ags_resume" initialView={initialView} />)
    expect(screen.getByText('Existing answer')).toBeInTheDocument()

    act(() => {
      const source = FakeEventSource.instances[0]
      source.open()
      source.emit('response.reasoning.delta', 'ags_resume', { delta: 'New thought' })
      source.emit('response.text.delta', 'ags_resume', { delta: 'New answer' })
    })
    expect(screen.getByText('New thought')).toBeInTheDocument()
    expect(screen.getByText('New answer')).toBeInTheDocument()
  })
})
