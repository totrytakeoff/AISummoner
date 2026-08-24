import {
  AGENT_CENTER_MIN,
  DOCK_DEFAULT,
  DOCK_MIN,
  SESSION_RAIL_WIDTH,
  SESSION_SIDEBAR_DEFAULT,
  clampPanelWidth,
  solveWorkspaceColumns,
} from './layout'

describe('workspace column concession', () => {
  it('keeps preferred panels when the center floor fits', () => {
    expect(solveWorkspaceColumns(1400, SESSION_SIDEBAR_DEFAULT, DOCK_DEFAULT, false, true, false)).toEqual({
      sessions: SESSION_SIDEBAR_DEFAULT,
      agent: 760,
      dock: DOCK_DEFAULT,
      dockAutoHidden: false,
    })
  })

  it('shrinks then auto-hides the optional dock before squeezing the session rail', () => {
    expect(solveWorkspaceColumns(1150, 280, 420, false, true, false)).toEqual({
      sessions: 280,
      agent: AGENT_CENTER_MIN,
      dock: 310,
      dockAutoHidden: false,
    })
    expect(solveWorkspaceColumns(1100, 280, 420, false, true, false)).toEqual({
      sessions: 280,
      agent: 820,
      dock: 0,
      dockAutoHidden: true,
    })
  })

  it('uses a fixed collapsed rail and gives a maximized dock the full frame', () => {
    expect(solveWorkspaceColumns(900, 300, DOCK_MIN, true, false, false)).toEqual({
      sessions: SESSION_RAIL_WIDTH,
      agent: 900 - SESSION_RAIL_WIDTH,
      dock: 0,
      dockAutoHidden: false,
    })
    expect(solveWorkspaceColumns(900, 300, DOCK_MIN, false, true, true)).toEqual({
      sessions: 0, agent: 0, dock: 900, dockAutoHidden: false,
    })
  })

  it('clamps invalid and out-of-range width preferences', () => {
    expect(clampPanelWidth(Number.NaN, 240, 400)).toBe(240)
    expect(clampPanelWidth(100, 240, 400)).toBe(240)
    expect(clampPanelWidth(900, 240, 400)).toBe(400)
  })
})
