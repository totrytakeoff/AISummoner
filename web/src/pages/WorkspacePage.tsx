import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { APIError, api } from '../api/client'
import type { AgentSession, AgentSessionSummary, Device } from '../api/types'
import { InlineError } from '../components/InlineError'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { StatusBadge } from '../components/StatusBadge'
import { ControllerSettingsDialog } from '../components/ControllerSettingsDialog'
import type { ControllerSettingsSection } from '../components/ControllerSettingsDialog'
import { PanelsIcon, SettingsIcon } from '../components/Icons'
import { useDevice } from '../devices/useDevice'
import { useAuth } from '../auth/AuthContext'
import { resolveAgentProviderPresentation } from '../agent/adapters'
import { AgentPage } from './AgentPage'
import { SessionRail } from '../workspace/SessionRail'
import { WorkspaceDock } from '../workspace/WorkspaceDock'
import type { WorkspaceDockTab } from '../workspace/WorkspaceDock'
import { WorkspaceFrame } from '../workspace/WorkspaceFrame'
import type { MobileWorkspacePanel } from '../workspace/WorkspaceFrame'
import {
  DOCK_DEFAULT,
  DOCK_MAX,
  DOCK_MIN,
  SESSION_SIDEBAR_DEFAULT,
  SESSION_SIDEBAR_MAX,
  SESSION_SIDEBAR_MIN,
  WORKSPACE_SINGLE_PANEL_MAX,
  clampPanelWidth,
} from '../workspace/layout'

const sessionWidthKey = 'aisummoner.workspace.session-width'
const dockWidthKey = 'aisummoner.workspace.dock-width'
const sessionsCollapsedKey = 'aisummoner.workspace.sessions-collapsed'

interface SessionIndexState {
  deviceID: string | undefined
  sessions: AgentSessionSummary[]
  loading: boolean
  error: string | null
}

interface SessionSelection {
  deviceID: string | undefined
  sessionID: string | null
}

function WorkspaceSettings({
  section,
  device,
  runtimeLabel,
  onClose,
  onUnpair,
  onArchivedSessionsChanged,
}: {
  section: ControllerSettingsSection
  device: Device
  runtimeLabel: string
  onClose: () => void
  onUnpair: () => Promise<void>
  onArchivedSessionsChanged: () => void
}) {
  const { user, logout } = useAuth()
  return (
    <ControllerSettingsDialog
      initialSection={section}
      device={device}
      runtimeLabel={runtimeLabel}
      username={user?.username || '账户'}
      onClose={onClose}
      onUnpair={onUnpair}
      onSignOut={logout}
      onArchivedSessionsChanged={onArchivedSessionsChanged}
    />
  )
}

function readNumberPreference(key: string, fallback: number, minimum: number, maximum: number): number {
  try {
    const value = Number(window.localStorage.getItem(key))
    return Number.isFinite(value) && value > 0 ? clampPanelWidth(value, minimum, maximum) : fallback
  } catch {
    return fallback
  }
}

function readBooleanPreference(key: string): boolean {
  try {
    return window.localStorage.getItem(key) === 'true'
  } catch {
    return false
  }
}

function writePreference(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Presentation preferences are optional when storage is unavailable.
  }
}

export function WorkspacePage() {
  const { deviceId } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const { device, loading, error } = useDevice(deviceId)
  const [index, setIndex] = useState<SessionIndexState>({ deviceID: deviceId, sessions: [], loading: true, error: null })
  const [selection, setSelection] = useState<SessionSelection>({ deviceID: deviceId, sessionID: null })
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [sessionActionError, setSessionActionError] = useState<string | null>(null)
  const [mutatingSessionID, setMutatingSessionID] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AgentSessionSummary | null>(null)
  const [sessionWidth, setSessionWidth] = useState(() => readNumberPreference(
    sessionWidthKey, SESSION_SIDEBAR_DEFAULT, SESSION_SIDEBAR_MIN, SESSION_SIDEBAR_MAX,
  ))
  const [dockWidth, setDockWidth] = useState(() => readNumberPreference(dockWidthKey, DOCK_DEFAULT, DOCK_MIN, DOCK_MAX))
  const [sessionsCollapsed, setSessionsCollapsed] = useState(() => readBooleanPreference(sessionsCollapsedKey))
  const requestedDock = new URLSearchParams(location.search).get('dock')
  const requestedSettings = new URLSearchParams(location.search).get('settings')
  const requestedSettingsSection: ControllerSettingsSection = requestedSettings === 'agent' || requestedSettings === 'device'
    ? requestedSettings
    : 'general'
  const [dockOpen, setDockOpen] = useState(requestedDock === 'terminal' || requestedDock === 'activity')
  const [dockTab, setDockTab] = useState<WorkspaceDockTab>(requestedDock === 'activity' ? 'activity' : 'terminal')
  const [dockMaximized, setDockMaximized] = useState(false)
  const [terminalMounted, setTerminalMounted] = useState(requestedDock === 'terminal')
  const [mobilePanel, setMobilePanel] = useState<MobileWorkspacePanel>(
    requestedDock === 'terminal' || requestedDock === 'activity' ? 'tools' : 'agent',
  )
  const [settingsOpen, setSettingsOpen] = useState(Boolean(requestedSettings))
  const [settingsSection, setSettingsSection] = useState<ControllerSettingsSection>(requestedSettingsSection)
  const requestSequence = useRef(0)
  const routeDeviceID = useRef(deviceId)
  const previousSessionID = useRef<string | null>(null)
  const creationsInFlight = useRef(new Set<string>())
  const automaticCreationAttempt = useRef<string | null>(null)
  const componentsTrigger = useRef<HTMLButtonElement>(null)
  const mobileAgentTrigger = useRef<HTMLButtonElement>(null)
  routeDeviceID.current = deviceId

  const sessions = index.deviceID === deviceId ? index.sessions : []
  const sessionsLoading = index.deviceID === deviceId ? index.loading : true
  const sessionsError = index.deviceID === deviceId ? index.error : null
  const selectedSessionID = selection.deviceID === deviceId ? selection.sessionID : null

  const refreshSessions = useCallback(async () => {
    const requestedDeviceID = deviceId
    const requestID = ++requestSequence.current
    if (!requestedDeviceID) return
    try {
      const next = await api.agentSessions(requestedDeviceID)
      if (requestSequence.current !== requestID) return
      if (next.some((session) => session.device_id !== requestedDeviceID)) {
        setIndex({ deviceID: requestedDeviceID, sessions: [], loading: false, error: '服务端返回了其他设备的会话。' })
        return
      }
      setIndex({ deviceID: requestedDeviceID, sessions: next, loading: false, error: null })
      setSelection((current) => {
        if (current.deviceID === requestedDeviceID && current.sessionID && next.some((session) => session.id === current.sessionID)) {
          return current
        }
        return { deviceID: requestedDeviceID, sessionID: next[0]?.id ?? null }
      })
    } catch (nextError) {
      if (requestSequence.current !== requestID) return
      setIndex((current) => ({
        deviceID: requestedDeviceID,
        sessions: current.deviceID === requestedDeviceID ? current.sessions : [],
        loading: false,
        error: nextError instanceof APIError ? nextError.message : '无法加载最近会话。',
      }))
    }
  }, [deviceId])

  useEffect(() => {
    ++requestSequence.current
    setIndex({ deviceID: deviceId, sessions: [], loading: true, error: null })
    setSelection({ deviceID: deviceId, sessionID: null })
    setCreating(deviceId ? creationsInFlight.current.has(deviceId) : false)
    setCreateError(null)
    setSessionActionError(null)
    setMutatingSessionID(null)
    setDeleteTarget(null)
    setDockOpen(requestedDock === 'terminal' || requestedDock === 'activity')
    setDockTab(requestedDock === 'activity' ? 'activity' : 'terminal')
    setDockMaximized(false)
    setTerminalMounted(requestedDock === 'terminal')
    setMobilePanel(requestedDock === 'terminal' || requestedDock === 'activity' ? 'tools' : 'agent')
    automaticCreationAttempt.current = null
    previousSessionID.current = null
    void refreshSessions()
    const timer = window.setInterval(() => void refreshSessions(), 5_000)
    return () => {
      window.clearInterval(timer)
      ++requestSequence.current
    }
  }, [deviceId, refreshSessions, requestedDock])

  useEffect(() => {
    setSettingsOpen(Boolean(requestedSettings))
    if (requestedSettings) setSettingsSection(requestedSettingsSection)
  }, [requestedSettings, requestedSettingsSection])

  useEffect(() => {
    const previous = previousSessionID.current
    if (previous && previous !== selectedSessionID) {
      setDockOpen(false)
      setDockMaximized(false)
      setTerminalMounted(false)
      setMobilePanel('agent')
    }
    previousSessionID.current = selectedSessionID
  }, [selectedSessionID])

  function updateSessionWidth(value: number) {
    const width = clampPanelWidth(value, SESSION_SIDEBAR_MIN, SESSION_SIDEBAR_MAX)
    setSessionWidth(width)
    writePreference(sessionWidthKey, String(width))
  }

  function updateDockWidth(value: number) {
    const width = clampPanelWidth(value, DOCK_MIN, DOCK_MAX)
    setDockWidth(width)
    writePreference(dockWidthKey, String(width))
  }

  function toggleSessions() {
    setSessionsCollapsed((current) => {
      const next = !current
      writePreference(sessionsCollapsedKey, String(next))
      return next
    })
  }

  function selectSession(sessionID: string) {
    setCreateError(null)
    setSelection({ deviceID: deviceId, sessionID })
    setMobilePanel('agent')
    if (window.innerWidth <= WORKSPACE_SINGLE_PANEL_MAX) {
      window.setTimeout(() => mobileAgentTrigger.current?.focus(), 0)
    }
  }

  const createSession = useCallback(async (restoreFocus = false) => {
    if (!device || !device.online) return
    const requestedDeviceID = device.id
    if (creationsInFlight.current.has(requestedDeviceID)) return
    creationsInFlight.current.add(requestedDeviceID)
    setCreating(true)
    setCreateError(null)
    try {
      const created = await api.createAgentSession(requestedDeviceID)
      if (routeDeviceID.current !== requestedDeviceID || created.device_id !== requestedDeviceID) return
      setSelection({ deviceID: requestedDeviceID, sessionID: created.id })
      setMobilePanel('agent')
      if (restoreFocus && window.innerWidth <= WORKSPACE_SINGLE_PANEL_MAX) {
        window.setTimeout(() => mobileAgentTrigger.current?.focus(), 0)
      }
      await refreshSessions()
    } catch (nextError) {
      if (routeDeviceID.current === requestedDeviceID) {
        setCreateError(nextError instanceof APIError ? nextError.message : '无法新建会话。')
      }
    } finally {
      creationsInFlight.current.delete(requestedDeviceID)
      if (routeDeviceID.current === requestedDeviceID) setCreating(false)
    }
  }, [device, refreshSessions])

  useEffect(() => {
    if (!device || !device.online || device.id !== deviceId || sessionsLoading || sessionsError ||
      sessions.length > 0 || selectedSessionID || automaticCreationAttempt.current === device.id) return
    automaticCreationAttempt.current = device.id
    void createSession()
  }, [createSession, device, deviceId, selectedSessionID, sessions, sessionsError, sessionsLoading])

  const handleSessionChange = useCallback((session: AgentSession) => {
    if (session.device_id !== deviceId) return
    setSelection((current) => current.deviceID === deviceId && current.sessionID === session.id
      ? current
      : { deviceID: deviceId, sessionID: session.id })
  }, [deviceId])

  const handleSessionIndexChanged = useCallback(() => {
    void refreshSessions()
  }, [refreshSessions])

  function removeSessionFromActiveIndex(sessionID: string) {
    const remaining = sessions.filter((session) => session.id !== sessionID)
    setIndex((current) => current.deviceID === deviceId
      ? { ...current, sessions: current.sessions.filter((session) => session.id !== sessionID) }
      : current)
    setSelection((current) => current.deviceID === deviceId && current.sessionID === sessionID
      ? { deviceID: deviceId, sessionID: remaining[0]?.id ?? null }
      : current)
  }

  async function archiveSession(summary: AgentSessionSummary) {
    if (mutatingSessionID) return
    setMutatingSessionID(summary.id)
    setSessionActionError(null)
    try {
      await api.setAgentSessionArchived(summary.id, true)
      removeSessionFromActiveIndex(summary.id)
      await refreshSessions()
    } catch (nextError) {
      setSessionActionError(nextError instanceof APIError ? nextError.message : '无法归档会话。')
    } finally {
      setMutatingSessionID(null)
    }
  }

  async function deleteSession(summary: AgentSessionSummary) {
    if (mutatingSessionID) return
    setMutatingSessionID(summary.id)
    setSessionActionError(null)
    try {
      await api.deleteAgentSession(summary.id)
      removeSessionFromActiveIndex(summary.id)
      setDeleteTarget(null)
      await refreshSessions()
    } catch (nextError) {
      setSessionActionError(nextError instanceof APIError ? nextError.message : '无法删除会话。')
    } finally {
      setMutatingSessionID(null)
    }
  }

  function openDock(tab: WorkspaceDockTab) {
    setDockTab(tab)
    setDockOpen(true)
    setDockMaximized(false)
    setMobilePanel('tools')
    if (tab === 'terminal') setTerminalMounted(true)
  }

  function toggleDock() {
    if (dockOpen && mobilePanel === 'tools') {
      closeDock()
      return
    }
    openDock(dockTab)
  }

  function openSettings(section: ControllerSettingsSection) {
    setSettingsSection(section)
    setSettingsOpen(true)
    const query = new URLSearchParams(location.search)
    query.set('settings', section)
    navigate({ pathname: location.pathname, search: `?${query.toString()}` }, { replace: true })
  }

  function closeSettings() {
    setSettingsOpen(false)
    const query = new URLSearchParams(location.search)
    query.delete('settings')
    const search = query.toString()
    navigate({ pathname: location.pathname, search: search ? `?${search}` : '' }, { replace: true })
  }

  function closeDock() {
    const restoreTarget = window.innerWidth <= WORKSPACE_SINGLE_PANEL_MAX
      ? mobileAgentTrigger.current
      : componentsTrigger.current
    setDockOpen(false)
    setDockMaximized(false)
    setTerminalMounted(false)
    setMobilePanel('agent')
    window.setTimeout(() => restoreTarget?.focus(), 0)
  }

  if (loading) return <div className="centered-state">正在打开控制工作区…</div>
  if (!device) return <div className="page-stack"><InlineError message={error || '设备不存在。'} /><Link className="back-link" to="/devices">← 返回设备</Link></div>

  return (
    <div className="control-workspace">
      <nav className="workspace-mobile-nav" aria-label="工作区面板">
        {(['sessions', 'agent', 'tools'] as const).map((panel) => (
          <button
            key={panel}
            ref={panel === 'agent' ? mobileAgentTrigger : undefined}
            type="button"
            data-active={mobilePanel === panel || undefined}
            onClick={() => panel === 'tools' ? openDock(dockTab) : setMobilePanel(panel)}
          >
            {panel === 'sessions' ? '会话' : panel === 'agent' ? 'Agent' : '组件'}
          </button>
        ))}
      </nav>

      <WorkspaceFrame
        sessionWidth={sessionWidth}
        dockWidth={dockWidth}
        sessionsCollapsed={sessionsCollapsed}
        dockOpen={dockOpen}
        dockMaximized={dockMaximized}
        mobilePanel={mobilePanel}
        onSessionWidth={updateSessionWidth}
        onDockWidth={updateDockWidth}
        sessions={(
          <SessionRail
            sessions={sessions}
            selectedSessionID={selectedSessionID}
            loading={sessionsLoading}
            creating={creating}
            online={device.online}
            error={sessionsError}
            mutationError={createError}
            actionError={sessionActionError}
            collapsed={sessionsCollapsed}
            onToggleCollapsed={toggleSessions}
            onSelect={selectSession}
            onArchive={(summary) => void archiveSession(summary)}
            onDelete={setDeleteTarget}
            onCreate={() => void createSession(true)}
            onRetryLoad={() => void refreshSessions()}
            onDismissMutationError={() => setCreateError(null)}
            onDismissActionError={() => setSessionActionError(null)}
            mutatingSessionID={mutatingSessionID}
            deviceName={device.name}
            onBackToDevices={() => navigate('/devices')}
            onOpenSettings={() => openSettings('general')}
          />
        )}
        agent={(
          <div className="workspace-agent-surface">
            <header className="workspace-toolbar">
              <div className="workspace-device-identity">
                <div><strong>{device.name}</strong><span>{device.platform} · {device.arch}</span></div>
                <StatusBadge online={device.online} />
              </div>
              <div className="workspace-toolbar-actions">
                <button
                  ref={componentsTrigger}
                  className="workspace-tool-button"
                  type="button"
                  aria-controls="workspace-components"
                  aria-expanded={dockOpen && mobilePanel === 'tools'}
                  onClick={toggleDock}
                >
                  <PanelsIcon /><span>组件</span>
                </button>
                <button className="icon-button" type="button" aria-label="打开设置" onClick={() => openSettings('general')}>
                  <SettingsIcon />
                </button>
              </div>
            </header>
            <div className="workspace-agent-body">
              {sessionsLoading && !selectedSessionID ? (
                <div className="centered-state workspace-loading">正在加载会话…</div>
              ) : selectedSessionID ? (
                <AgentPage
                  embedded
                  selectedSessionID={selectedSessionID}
                  onSessionChange={handleSessionChange}
                  onSessionIndexChanged={handleSessionIndexChanged}
                  onOpenSettings={() => openSettings('agent')}
                />
              ) : (
                <section className="workspace-empty-conversation" aria-labelledby="empty-conversation-title">
                  <span aria-hidden="true">✦</span>
                  <h2 id="empty-conversation-title">{device.online ? creating ? '正在新建会话…' : '还没有会话' : '设备离线'}</h2>
                  <p>{device.online
                    ? createError || sessionsError || '系统将自动创建一个逐条确认命令的会话。'
                    : '设备重新连接后即可查看会话历史。'}</p>
                  {device.online && !creating && (
                    <button className="button primary" type="button" onClick={() => void createSession(true)}>重试</button>
                  )}
                </section>
              )}
            </div>
          </div>
        )}
        dock={(
          <WorkspaceDock
            device={device}
            tab={dockTab}
            terminalMounted={terminalMounted}
            maximized={dockMaximized}
            onTab={openDock}
            onClose={closeDock}
            onToggleMaximized={() => {
              setDockOpen(true)
              setDockMaximized((current) => !current)
            }}
          />
        )}
      />
      {settingsOpen && (
        <WorkspaceSettings
          section={settingsSection}
          device={device}
          runtimeLabel={resolveAgentProviderPresentation(sessions.find((session) => session.id === selectedSessionID)?.provider).label}
          onClose={closeSettings}
          onUnpair={async () => {
            await api.unpair(device.id)
            navigate('/devices', { replace: true })
          }}
          onArchivedSessionsChanged={() => void refreshSessions()}
        />
      )}
      {deleteTarget && (
        <ConfirmDialog
          eyebrow="删除会话"
          title={`永久删除“${deleteTarget.title}”？`}
          description={<p>该会话在 AISummoner 中保存的消息与命令记录会一并删除，无法恢复。若只想从列表隐藏，请使用归档。</p>}
          confirmLabel="永久删除"
          busyLabel="正在删除…"
          busy={mutatingSessionID === deleteTarget.id}
          onCancel={() => {
            if (!mutatingSessionID) setDeleteTarget(null)
          }}
          onConfirm={() => void deleteSession(deleteTarget)}
        />
      )}
    </div>
  )
}
