import { startTerminalSession } from './session'
import type { FitAddonLike, ResizeObserverLike, TerminalLike, TerminalSocketLike } from './session'

class FakeSocket extends EventTarget implements TerminalSocketLike {
  readyState: number = WebSocket.CONNECTING
  binaryType: BinaryType = 'blob'
  sent: Array<string | ArrayBufferView> = []
  close = vi.fn(() => { this.readyState = WebSocket.CLOSED })

  send(data: string | ArrayBufferView) { this.sent.push(data) }
  open() { this.readyState = WebSocket.OPEN; this.dispatchEvent(new Event('open')) }
  receive(data: unknown) { this.dispatchEvent(new MessageEvent('message', { data })) }
}

function fixture() {
  let input: ((data: string) => void) | undefined
  let resize: ResizeObserverCallback | undefined
  const inputDispose = vi.fn()
  const terminal: TerminalLike = {
    cols: 120,
    rows: 36,
    open: vi.fn(),
    loadAddon: vi.fn(),
    onData: vi.fn((callback) => { input = callback; return { dispose: inputDispose } }),
    write: vi.fn(),
    dispose: vi.fn(),
  }
  const fitAddon: FitAddonLike = { fit: vi.fn(), activate: vi.fn(), dispose: vi.fn() }
  const observer: ResizeObserverLike = { observe: vi.fn(), disconnect: vi.fn() }
  const socket = new FakeSocket()
  const removeEventListener = vi.spyOn(socket, 'removeEventListener')
  const state = vi.fn()
  const container = document.createElement('div')
  const dispose = startTerminalSession({
    container,
    url: 'ws://example.test/terminal',
    onState: state,
    terminalFactory: () => terminal,
    fitAddonFactory: () => fitAddon,
    socketFactory: () => socket,
    resizeObserverFactory: (callback) => { resize = callback; return observer },
  })
  return {
    terminal,
    fitAddon,
    observer,
    socket,
    state,
    dispose,
    inputDispose,
    removeEventListener,
    input: () => input!,
    resize: () => resize!([], observer as ResizeObserver),
  }
}

describe('terminal session boundary', () => {
  it('bridges binary data and bounded resize controls', () => {
    const session = fixture()
    session.socket.open()
    expect(session.socket.binaryType).toBe('arraybuffer')
    expect(session.socket.sent[0]).toBe(JSON.stringify({ type: 'terminal.resize', cols: 120, rows: 36 }))

    session.input()('ls\n')
    expect(ArrayBuffer.isView(session.socket.sent[1])).toBe(true)
    const sent = session.socket.sent[1] as ArrayBufferView
    expect(new TextDecoder().decode(new Uint8Array(sent.buffer, sent.byteOffset, sent.byteLength))).toBe('ls\n')

    session.socket.receive(new Uint8Array([65, 66]).buffer)
    expect(session.terminal.write).toHaveBeenCalledWith(new Uint8Array([65, 66]))
    expect(session.state).toHaveBeenCalledWith('connected')
  })

  it('clamps observed resize controls and chunks encoded input at the frame boundary', () => {
    const session = fixture()
    session.terminal.cols = 900
    session.terminal.rows = -2
    session.socket.open()
    expect(session.socket.sent[0]).toBe(JSON.stringify({ type: 'terminal.resize', cols: 500, rows: 1 }))

    session.terminal.cols = 88
    session.terminal.rows = 42
    session.resize()
    expect(session.socket.sent[1]).toBe(JSON.stringify({ type: 'terminal.resize', cols: 88, rows: 42 }))

    session.input()('x'.repeat(64 * 1024 + 1))
    const chunks = session.socket.sent.slice(2) as ArrayBufferView[]
    expect(chunks).toHaveLength(2)
    expect(chunks[0].byteLength).toBe(64 * 1024)
    expect(chunks[1].byteLength).toBe(1)
  })

  it('removes every listener and closes the input, socket, observer and terminal on cleanup', () => {
    const session = fixture()
    session.socket.open()
    const sentBeforeCleanup = session.socket.sent.length
    session.dispose()

    expect(session.observer.disconnect).toHaveBeenCalledOnce()
    expect(session.inputDispose).toHaveBeenCalledOnce()
    expect(session.removeEventListener).toHaveBeenCalledTimes(4)
    for (const eventName of ['open', 'error', 'close', 'message']) {
      expect(session.removeEventListener).toHaveBeenCalledWith(eventName, expect.any(Function))
    }
    expect(session.socket.close).toHaveBeenCalledWith(1000, 'page closed')
    expect(session.terminal.dispose).toHaveBeenCalledOnce()

    session.input()('ignored after cleanup')
    session.socket.receive(new Uint8Array([90]).buffer)
    expect(session.socket.sent).toHaveLength(sentBeforeCleanup)
    expect(session.terminal.write).not.toHaveBeenCalledWith(new Uint8Array([90]))
  })
})
