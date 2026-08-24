export const SESSION_RAIL_WIDTH = 56
export const SESSION_SIDEBAR_MIN = 240
export const SESSION_SIDEBAR_MAX = 400
export const SESSION_SIDEBAR_DEFAULT = 280
export const DOCK_MIN = 300
export const DOCK_MAX = 560
export const DOCK_DEFAULT = 360
export const AGENT_CENTER_MIN = 560
export const WORKSPACE_SINGLE_PANEL_MAX = SESSION_SIDEBAR_MAX + AGENT_CENTER_MIN + DOCK_MIN - 1

export interface WorkspaceColumns {
  sessions: number
  agent: number
  dock: number
  dockAutoHidden: boolean
}

export function clampPanelWidth(value: number, minimum: number, maximum: number): number {
  if (!Number.isFinite(value)) return minimum
  return Math.min(maximum, Math.max(minimum, Math.round(value)))
}

export function solveWorkspaceColumns(
  viewport: number,
  sessionPreference: number,
  dockPreference: number,
  sessionsCollapsed: boolean,
  dockOpen: boolean,
  dockMaximized: boolean,
): WorkspaceColumns {
  const width = Math.max(0, Math.round(viewport))
  if (dockOpen && dockMaximized) {
    return { sessions: 0, agent: 0, dock: width, dockAutoHidden: false }
  }

  const sessions = sessionsCollapsed
    ? SESSION_RAIL_WIDTH
    : clampPanelWidth(sessionPreference, SESSION_SIDEBAR_MIN, SESSION_SIDEBAR_MAX)
  if (!dockOpen) {
    return { sessions, agent: Math.max(0, width - sessions), dock: 0, dockAutoHidden: false }
  }

  const preferredDock = clampPanelWidth(dockPreference, DOCK_MIN, DOCK_MAX)
  if (sessions + AGENT_CENTER_MIN + preferredDock <= width) {
    return { sessions, agent: width - sessions - preferredDock, dock: preferredDock, dockAutoHidden: false }
  }
  const reducedDock = Math.max(DOCK_MIN, width - sessions - AGENT_CENTER_MIN)
  if (sessions + AGENT_CENTER_MIN + reducedDock <= width) {
    return { sessions, agent: AGENT_CENTER_MIN, dock: reducedDock, dockAutoHidden: false }
  }
  return { sessions, agent: Math.max(0, width - sessions), dock: 0, dockAutoHidden: true }
}
