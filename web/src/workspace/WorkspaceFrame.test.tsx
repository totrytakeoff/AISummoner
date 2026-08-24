import { fireEvent, render, screen } from '@testing-library/react'
import { WorkspaceFrame } from './WorkspaceFrame'

describe('WorkspaceFrame accessibility', () => {
  it('resizes both panels with keyboard movement semantics', async () => {
    const onSessionWidth = vi.fn()
    const onDockWidth = vi.fn()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })
    render(
      <WorkspaceFrame
        sessionWidth={280}
        dockWidth={360}
        sessionsCollapsed={false}
        dockOpen
        dockMaximized={false}
        mobilePanel="agent"
        onSessionWidth={onSessionWidth}
        onDockWidth={onDockWidth}
        sessions={<div>sessions</div>}
        agent={<div>agent</div>}
        dock={<div>dock</div>}
      />,
    )

    const sessions = screen.getByRole('separator', { name: '调整会话栏宽度' })
    expect(sessions).toHaveAttribute('aria-valuenow', '280')
    fireEvent.keyDown(sessions, { key: 'ArrowRight' })
    fireEvent.keyDown(sessions, { key: 'Home' })
    fireEvent.keyDown(sessions, { key: 'End' })
    expect(onSessionWidth.mock.calls.map(([value]) => value)).toEqual([296, 240, 400])

    const dock = screen.getByRole('separator', { name: '调整组件栏宽度' })
    fireEvent.keyDown(dock, { key: 'ArrowLeft' })
    fireEvent.keyDown(dock, { key: 'ArrowRight' })
    expect(onDockWidth.mock.calls.map(([value]) => value)).toEqual([376, 344])
  })

  it('removes resize handles for collapsed or maximized boundaries', () => {
    render(
      <WorkspaceFrame
        sessionWidth={280}
        dockWidth={360}
        sessionsCollapsed
        dockOpen
        dockMaximized
        mobilePanel="tools"
        onSessionWidth={() => {}}
        onDockWidth={() => {}}
        sessions={<div>sessions</div>}
        agent={<div>agent</div>}
        dock={<div>dock</div>}
      />,
    )
    expect(screen.queryByRole('separator')).not.toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: '工作区组件' }).parentElement).toHaveAttribute('data-dock-maximized', 'true')
    expect(document.querySelector('.workspace-sessions-column')).toHaveAttribute('inert')
    const agentRegion = document.querySelector('.workspace-agent-column')
    expect(agentRegion).toHaveAttribute('inert')
    expect(agentRegion?.tagName).toBe('SECTION')
    expect(agentRegion).toHaveAttribute('aria-label', 'Agent 工作区')
    expect(document.querySelector('main')).not.toBeInTheDocument()
  })

  it('captures pointer movement across the actual separator boundary', () => {
    const onSessionWidth = vi.fn()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })
    render(
      <WorkspaceFrame
        sessionWidth={280}
        dockWidth={360}
        sessionsCollapsed={false}
        dockOpen={false}
        dockMaximized={false}
        mobilePanel="sessions"
        onSessionWidth={onSessionWidth}
        onDockWidth={() => {}}
        sessions={<div>sessions</div>}
        agent={<div>agent</div>}
        dock={<div>dock</div>}
      />,
    )

    const separator = screen.getByRole('separator', { name: '调整会话栏宽度' })
    let captured = false
    Object.defineProperties(separator, {
      setPointerCapture: { value: () => { captured = true } },
      hasPointerCapture: { value: () => captured },
      releasePointerCapture: { value: () => { captured = false } },
    })
    const dispatchPointer = (type: string, clientX: number) => {
      const event = new Event(type, { bubbles: true, cancelable: true })
      Object.defineProperties(event, {
        pointerId: { value: 7 },
        clientX: { value: clientX },
      })
      fireEvent(separator, event)
    }
    dispatchPointer('pointerdown', 280)
    dispatchPointer('pointermove', 320)
    dispatchPointer('pointerup', 320)
    expect(onSessionWidth).toHaveBeenCalledWith(320)
    expect(captured).toBe(false)
  })

  it('keeps only the selected narrow-screen panel in the accessibility tree', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    render(
      <WorkspaceFrame
        sessionWidth={280}
        dockWidth={360}
        sessionsCollapsed={false}
        dockOpen
        dockMaximized={false}
        mobilePanel="tools"
        onSessionWidth={() => {}}
        onDockWidth={() => {}}
        sessions={<div>sessions</div>}
        agent={<div>agent</div>}
        dock={<div>dock</div>}
      />,
    )
    expect(screen.getByRole('complementary', { name: '工作区组件' })).not.toHaveAttribute('aria-hidden')
    expect(document.querySelector('.workspace-sessions-column')).toHaveAttribute('aria-hidden', 'true')
    expect(document.querySelector('.workspace-sessions-column')).toHaveAttribute('inert')
    expect(document.querySelector('.workspace-agent-column')).toHaveAttribute('aria-hidden', 'true')
    expect(document.querySelector('.workspace-agent-column')).toHaveAttribute('inert')
    expect(document.querySelector('.workspace-dock-column')).not.toHaveAttribute('inert')
  })

  it.each([1140, 1259])('uses the visible Tools panel at %ipx with the maximum persisted rail', (width) => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
    render(
      <WorkspaceFrame
        sessionWidth={400}
        dockWidth={560}
        sessionsCollapsed={false}
        dockOpen
        dockMaximized={false}
        mobilePanel="tools"
        onSessionWidth={() => {}}
        onDockWidth={() => {}}
        sessions={<button type="button">session action</button>}
        agent={<button type="button">agent action</button>}
        dock={<button type="button">terminal action</button>}
      />,
    )
    expect(document.querySelector('.workspace-frame')).toHaveAttribute('data-single-panel', 'true')
    expect(screen.getByRole('button', { name: 'terminal action' })).toBeEnabled()
    expect(document.querySelector('.workspace-dock-column')).not.toHaveAttribute('aria-hidden')
    expect(document.querySelector('.workspace-dock-column')).not.toHaveAttribute('inert')
    expect(document.querySelector('.workspace-sessions-column')).toHaveAttribute('inert')
    expect(document.querySelector('.workspace-agent-column')).toHaveAttribute('inert')
  })

  it('returns to the minimum usable three-column layout at 1260px', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1260 })
    render(
      <WorkspaceFrame
        sessionWidth={400}
        dockWidth={560}
        sessionsCollapsed={false}
        dockOpen
        dockMaximized={false}
        mobilePanel="tools"
        onSessionWidth={() => {}}
        onDockWidth={() => {}}
        sessions={<button type="button">session action</button>}
        agent={<button type="button">agent action</button>}
        dock={<button type="button">terminal action</button>}
      />,
    )
    expect(document.querySelector('.workspace-frame')).not.toHaveAttribute('data-single-panel')
    expect(document.querySelector('.workspace-sessions-column')).not.toHaveAttribute('inert')
    expect(document.querySelector('.workspace-agent-column')).not.toHaveAttribute('inert')
    expect(document.querySelector('.workspace-dock-column')).not.toHaveAttribute('inert')
    expect(screen.getByRole('separator', { name: '调整组件栏宽度' })).toHaveAttribute('aria-valuenow', '300')
  })

  it('reports the rendered dock width after desktop concession', () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1150 })
    render(
      <WorkspaceFrame
        sessionWidth={280}
        dockWidth={420}
        sessionsCollapsed={false}
        dockOpen
        dockMaximized={false}
        mobilePanel="agent"
        onSessionWidth={() => {}}
        onDockWidth={() => {}}
        sessions={<div>sessions</div>}
        agent={<div>agent</div>}
        dock={<div>dock</div>}
      />,
    )
    expect(screen.getByRole('separator', { name: '调整组件栏宽度' })).toHaveAttribute('aria-valuenow', '310')
  })
})
