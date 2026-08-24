import { useCallback, useEffect, useRef, useState } from 'react'
import type { KeyboardEvent, PointerEvent, ReactNode } from 'react'
import {
  DOCK_MAX,
  DOCK_MIN,
  SESSION_SIDEBAR_MAX,
  SESSION_SIDEBAR_MIN,
  WORKSPACE_SINGLE_PANEL_MAX,
  solveWorkspaceColumns,
} from './layout'

export type MobileWorkspacePanel = 'sessions' | 'agent' | 'tools'

interface WorkspaceFrameProps {
  sessionWidth: number
  dockWidth: number
  sessionsCollapsed: boolean
  dockOpen: boolean
  dockMaximized: boolean
  mobilePanel: MobileWorkspacePanel
  onSessionWidth: (width: number) => void
  onDockWidth: (width: number) => void
  sessions: ReactNode
  agent: ReactNode
  dock: ReactNode
}

interface ResizeHandleProps {
  side: 'sessions' | 'dock'
  position: number
  value: number
  minimum: number
  maximum: number
  onChange: (value: number) => void
}

function ResizeHandle({ side, position, value, minimum, maximum, onChange }: ResizeHandleProps) {
  const origin = useRef(0)
  const initial = useRef(value)
  const [dragging, setDragging] = useState(false)

  const move = useCallback((clientX: number) => {
    const delta = clientX - origin.current
    onChange(initial.current + (side === 'sessions' ? delta : -delta))
  }, [onChange, side])

  function pointerDown(event: PointerEvent<HTMLDivElement>) {
    event.preventDefault()
    origin.current = event.clientX
    initial.current = value
    event.currentTarget.setPointerCapture(event.pointerId)
    setDragging(true)
  }

  function pointerMove(event: PointerEvent<HTMLDivElement>) {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) move(event.clientX)
  }

  function pointerEnd(event: PointerEvent<HTMLDivElement>) {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      move(event.clientX)
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    setDragging(false)
  }

  function keyDown(event: KeyboardEvent<HTMLDivElement>) {
    let next: number | undefined
    if (event.key === 'Home') next = minimum
    if (event.key === 'End') next = maximum
    if (event.key === 'ArrowLeft') next = value + (side === 'sessions' ? -16 : 16)
    if (event.key === 'ArrowRight') next = value + (side === 'sessions' ? 16 : -16)
    if (next === undefined) return
    event.preventDefault()
    onChange(next)
  }

  return (
    <div
      className="workspace-resizer"
      data-side={side}
      data-dragging={dragging || undefined}
      style={{ left: position }}
      role="separator"
      aria-label={side === 'sessions' ? '调整会话栏宽度' : '调整组件栏宽度'}
      aria-orientation="vertical"
      aria-valuemin={minimum}
      aria-valuemax={maximum}
      aria-valuenow={value}
      tabIndex={0}
      onKeyDown={keyDown}
      onPointerDown={pointerDown}
      onPointerMove={pointerMove}
      onPointerUp={pointerEnd}
      onPointerCancel={pointerEnd}
    />
  )
}

export function WorkspaceFrame(props: WorkspaceFrameProps) {
  const frameRef = useRef<HTMLDivElement>(null)
  const [viewport, setViewport] = useState(() => window.innerWidth)

  useEffect(() => {
    const frame = frameRef.current
    if (!frame) return
    const update = () => {
      const width = frame.getBoundingClientRect().width
      if (width > 0) setViewport(width)
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(frame)
    return () => observer.disconnect()
  }, [])

  const columns = solveWorkspaceColumns(
    viewport,
    props.sessionWidth,
    props.dockWidth,
    props.sessionsCollapsed,
    props.dockOpen,
    props.dockMaximized,
  )
  const narrow = viewport <= WORKSPACE_SINGLE_PANEL_MAX
  const sessionsHidden = narrow ? props.mobilePanel !== 'sessions' : columns.sessions === 0
  const agentHidden = narrow ? props.mobilePanel !== 'agent' : columns.agent === 0
  const dockHidden = narrow ? props.mobilePanel !== 'tools' : columns.dock === 0

  return (
    <div
      ref={frameRef}
      className="workspace-frame"
      data-mobile-panel={props.mobilePanel}
      data-sessions-collapsed={props.sessionsCollapsed || undefined}
      data-dock-collapsed={columns.dock === 0 || undefined}
      data-dock-auto-hidden={columns.dockAutoHidden || undefined}
      data-dock-maximized={props.dockMaximized || undefined}
      data-single-panel={narrow || undefined}
      style={{ gridTemplateColumns: `${columns.sessions}px minmax(0, 1fr) ${columns.dock}px` }}
    >
      <aside className="workspace-sessions-column" aria-label="Agent 会话" aria-hidden={sessionsHidden || undefined} inert={sessionsHidden || undefined}>{props.sessions}</aside>
      <section className="workspace-agent-column" aria-label="Agent 工作区" aria-hidden={agentHidden || undefined} inert={agentHidden || undefined}>{props.agent}</section>
      <aside className="workspace-dock-column" aria-label="工作区组件" aria-hidden={dockHidden || undefined} inert={dockHidden || undefined}>{props.dock}</aside>
      {!props.sessionsCollapsed && !props.dockMaximized && (
        <ResizeHandle
          side="sessions"
          position={columns.sessions}
          value={columns.sessions}
          minimum={SESSION_SIDEBAR_MIN}
          maximum={SESSION_SIDEBAR_MAX}
          onChange={props.onSessionWidth}
        />
      )}
      {columns.dock > 0 && !props.dockMaximized && (
        <ResizeHandle
          side="dock"
          position={viewport - columns.dock}
          value={columns.dock}
          minimum={DOCK_MIN}
          maximum={DOCK_MAX}
          onChange={props.onDockWidth}
        />
      )}
    </div>
  )
}
