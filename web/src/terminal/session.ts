import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import type { ITerminalAddon } from '@xterm/xterm'

const MAX_TERMINAL_FRAME = 64 * 1024
const MAX_COLS = 500
const MAX_ROWS = 300

export type TerminalConnectionState = 'connecting' | 'connected' | 'closed' | 'error'

export interface TerminalLike {
  cols: number
  rows: number
  open: (element: HTMLElement) => void
  loadAddon: (addon: ITerminalAddon) => void
  onData: (callback: (data: string) => void) => { dispose: () => void }
  write: (data: string | Uint8Array) => void
  dispose: () => void
}

export interface FitAddonLike extends ITerminalAddon {
  fit: () => void
}

export interface ResizeObserverLike {
  observe: (target: Element) => void
  disconnect: () => void
}

export interface TerminalSocketLike {
  readyState: number
  binaryType: BinaryType
  send: (data: string | ArrayBufferView) => void
  close: (code?: number, reason?: string) => void
  addEventListener: (type: string, listener: EventListener) => void
  removeEventListener: (type: string, listener: EventListener) => void
}

interface StartTerminalOptions {
  container: HTMLElement
  url: string
  onState: (state: TerminalConnectionState, detail?: string) => void
  terminalFactory?: () => TerminalLike
  fitAddonFactory?: () => FitAddonLike
  socketFactory?: (url: string) => TerminalSocketLike
  resizeObserverFactory?: (callback: ResizeObserverCallback) => ResizeObserverLike
}

function defaultTerminalFactory(): TerminalLike {
  return new Terminal({
    cursorBlink: true,
    convertEol: true,
    fontFamily: "'JetBrains Mono', 'SFMono-Regular', Consolas, monospace",
    fontSize: 14,
    theme: {
      background: '#070d18',
      foreground: '#d9e5f2',
      cursor: '#62e6c7',
      selectionBackground: '#28526b',
    },
  })
}

function defaultFitAddonFactory(): FitAddonLike {
  return new FitAddon()
}

function defaultSocketFactory(url: string): TerminalSocketLike {
  return new WebSocket(url)
}

function defaultResizeObserverFactory(callback: ResizeObserverCallback): ResizeObserverLike {
  return new ResizeObserver(callback)
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.max(minimum, Math.min(maximum, Math.trunc(value)))
}

function binaryBytes(value: unknown): Uint8Array | null {
  if (value instanceof ArrayBuffer) return new Uint8Array(value)
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
  return null
}

export function startTerminalSession(options: StartTerminalOptions): () => void {
  const terminal = (options.terminalFactory ?? defaultTerminalFactory)()
  const fitAddon = (options.fitAddonFactory ?? defaultFitAddonFactory)()
  const socket = (options.socketFactory ?? defaultSocketFactory)(options.url)
  const observer = (options.resizeObserverFactory ?? defaultResizeObserverFactory)(resize)
  const encoder = new TextEncoder()
  let active = true

  terminal.loadAddon(fitAddon)
  terminal.open(options.container)
  socket.binaryType = 'arraybuffer'
  options.onState('connecting')

  function sendResize() {
    if (!active || socket.readyState !== WebSocket.OPEN) return
    const cols = clamp(terminal.cols, 1, MAX_COLS)
    const rows = clamp(terminal.rows, 1, MAX_ROWS)
    socket.send(JSON.stringify({ type: 'terminal.resize', cols, rows }))
  }

  function resize() {
    if (!active) return
    try {
      fitAddon.fit()
      sendResize()
    } catch {
      // xterm can be briefly unmeasurable while its route is mounting.
    }
  }

  function opened() {
    if (!active) return
    options.onState('connected')
    resize()
  }

  function failed() {
    if (!active) return
    options.onState('error', 'The terminal connection encountered an error.')
  }

  function closed(event: Event) {
    if (!active) return
    const closeEvent = event as CloseEvent
    const detail = closeEvent.reason || (closeEvent.code === 1000 ? 'Terminal closed.' : 'Terminal connection closed.')
    options.onState('closed', detail)
  }

  function textControl(data: string) {
    try {
      const message = JSON.parse(data) as { type?: unknown; message?: unknown }
      if (message.type === 'terminal.error' && typeof message.message === 'string') {
        options.onState('error', message.message)
      }
    } catch {
      // Text frames are reserved for controls; malformed controls are not rendered as shell output.
    }
  }

  function message(event: Event) {
    if (!active) return
    const data = (event as MessageEvent).data as unknown
    if (typeof data === 'string') {
      textControl(data)
      return
    }
    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => {
        if (active) terminal.write(new Uint8Array(buffer))
      })
      return
    }
    const bytes = binaryBytes(data)
    if (bytes) terminal.write(bytes)
  }

  socket.addEventListener('open', opened)
  socket.addEventListener('error', failed)
  socket.addEventListener('close', closed)
  socket.addEventListener('message', message)
  observer.observe(options.container)

  const inputDisposable = terminal.onData((input) => {
    if (!active || socket.readyState !== WebSocket.OPEN) return
    const bytes = encoder.encode(input)
    for (let offset = 0; offset < bytes.byteLength; offset += MAX_TERMINAL_FRAME) {
      socket.send(bytes.subarray(offset, offset + MAX_TERMINAL_FRAME))
    }
  })

  return () => {
    if (!active) return
    active = false
    observer.disconnect()
    inputDisposable.dispose()
    socket.removeEventListener('open', opened)
    socket.removeEventListener('error', failed)
    socket.removeEventListener('close', closed)
    socket.removeEventListener('message', message)
    if (socket.readyState === WebSocket.CONNECTING || socket.readyState === WebSocket.OPEN) {
      socket.close(1000, 'page closed')
    }
    terminal.dispose()
  }
}
