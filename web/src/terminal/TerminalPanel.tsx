import { useEffect, useRef, useState } from 'react'
import { terminalWebSocketURL } from '../api/client'
import { startTerminalSession } from './session'
import type { TerminalConnectionState } from './session'
import '@xterm/xterm/css/xterm.css'

export function TerminalPanel({ deviceID }: { deviceID: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<TerminalConnectionState>('connecting')
  const [detail, setDetail] = useState<string | undefined>()

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    return startTerminalSession({
      container,
      url: terminalWebSocketURL(deviceID),
      onState: (nextState, nextDetail) => {
        setState(nextState)
        setDetail(nextDetail)
      },
    })
  }, [deviceID])

  return (
    <section className="terminal-frame" aria-label="Remote terminal">
      <div className="terminal-toolbar">
        <span className={`connection-indicator ${state}`} aria-hidden="true" />
        <span className="mono">{state === 'connected' ? 'Connected' : state}</span>
        {detail && <span className="terminal-detail" role={state === 'error' ? 'alert' : 'status'}>{detail}</span>}
      </div>
      <div className="terminal-container" ref={containerRef} />
    </section>
  )
}
