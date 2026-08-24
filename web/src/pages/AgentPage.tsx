import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { APIError, api } from '../api/client'
import type { AgentSession, ApprovalMode, DSHCredentialStatus } from '../api/types'
import type { ToolDecision } from '../api/types'
import { resolveAgentProviderPresentation } from '../agent/adapters'
import { initialAgentViewState, projectAgentSnapshot } from '../agent/events'
import type { AgentViewState } from '../agent/events'
import { ReasoningBlock } from '../agent/ReasoningBlock'
import { ToolApprovalPanel } from '../agent/ToolApprovalPanel'
import { ToolCallCard } from '../agent/ToolCallCard'
import { useAgentEvents } from '../agent/useAgentEvents'
import { InlineError } from '../components/InlineError'
import { ChevronDownIcon, ModelIcon, SendIcon, SettingsIcon, SparklesIcon } from '../components/Icons'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useDevice } from '../devices/useDevice'

interface ScopedSession {
  deviceID: string
  session: AgentSession
  initialView: AgentViewState
}

interface AgentPageProps {
  embedded?: boolean
  selectedSessionID?: string | null
  onSessionChange?: (session: AgentSession) => void
  onSessionIndexChanged?: () => void
  onOpenSettings?: () => void
}

export function AgentPage({
  embedded = false,
  selectedSessionID = null,
  onSessionChange,
  onSessionIndexChanged,
  onOpenSettings,
}: AgentPageProps = {}) {
  const { deviceId } = useParams()
  const { device, loading, error } = useDevice(deviceId)
  const [scopedSession, setScopedSession] = useState<ScopedSession | null>(null)
  const [creating, setCreating] = useState(false)
  const [sessionLoading, setSessionLoading] = useState(true)
  const [sessionReload, setSessionReload] = useState(0)
  const [sessionError, setSessionError] = useState<string | null>(null)
  const [prompt, setPrompt] = useState('')
  const [sending, setSending] = useState(false)
  const [credentialStatus, setCredentialStatus] = useState<DSHCredentialStatus | null>(null)
  const [credentialError, setCredentialError] = useState<string | null>(null)
  const [credentialFailureSuppressed, setCredentialFailureSuppressed] = useState(false)
  const [permissionOpen, setPermissionOpen] = useState(false)
  const [permissionChanging, setPermissionChanging] = useState(false)
  const [confirmFullAccess, setConfirmFullAccess] = useState(false)
  const [collapseSignal, setCollapseSignal] = useState(0)
  const session = scopedSession && scopedSession.deviceID === deviceId && scopedSession.session.device_id === deviceId
    ? scopedSession.session
    : null
  const {
    state,
    streamState,
    beginUserMessage,
    acceptUserMessage,
    rejectUserMessage,
    markDecision,
  } = useAgentEvents(session?.id ?? null, scopedSession?.initialView ?? initialAgentViewState)
  const provider = resolveAgentProviderPresentation(session?.provider)
  const pendingTool = useMemo(() => {
    for (let index = state.timeline.length - 1; index >= 0; index--) {
      const item = state.timeline[index]
      if (item?.kind === 'tool' && item.tool.status === 'pending' && !item.tool.decision) return item.tool
    }
    return undefined
  }, [state.timeline])
  const hasTools = useMemo(() => state.timeline.some((item) => item.kind === 'tool'), [state.timeline])
  const endRef = useRef<HTMLDivElement>(null)
  const routeDeviceID = useRef(deviceId)
  const activeSessionID = useRef<string | null>(session?.id ?? null)
  routeDeviceID.current = deviceId
  activeSessionID.current = session?.id ?? null

  useEffect(() => {
    setScopedSession(null)
    setCreating(false)
    setSessionLoading(true)
    setSessionReload(0)
    setSessionError(null)
    setPrompt('')
    setSending(false)
    setCredentialStatus(null)
    setCredentialError(null)
    setCredentialFailureSuppressed(false)
    setPermissionOpen(false)
    setConfirmFullAccess(false)
  }, [deviceId])

  useEffect(() => {
    setCredentialStatus(null)
    setCredentialError(null)
    setCredentialFailureSuppressed(false)
    if (session?.provider !== 'dsh') {
      return
    }
    let current = true
    async function refreshCredential() {
      try {
        const next = await api.dshCredentialStatus()
        if (!current) return
        setCredentialStatus(next)
        setCredentialError(null)
        if (!next.configured) setCredentialFailureSuppressed(true)
      } catch (nextError) {
        if (!current) return
        setCredentialStatus(null)
        setCredentialError(nextError instanceof APIError ? nextError.message : '无法确认 DSH 凭据状态。')
      }
    }
    void refreshCredential()
    const changed = () => void refreshCredential()
    window.addEventListener('aisummoner:dsh-credential-changed', changed)
    return () => {
      current = false
      window.removeEventListener('aisummoner:dsh-credential-changed', changed)
    }
  }, [session?.id, session?.provider])

  useEffect(() => {
    if (!deviceId || !device || device.id !== deviceId) return
    const requestedDeviceID = device.id
    let current = true
    if (selectedSessionID) {
      setScopedSession((existing) => existing?.session.id === selectedSessionID ? existing : null)
      setPrompt('')
    }
    setSessionLoading(true)
    setSessionError(null)
    void (async () => {
      try {
        const snapshot = selectedSessionID
          ? await api.agentSession(selectedSessionID)
          : await api.latestAgentSession(requestedDeviceID)
        if (!current || routeDeviceID.current !== requestedDeviceID) return
        if (snapshot.session.device_id !== requestedDeviceID) {
          setSessionError('服务端返回了其他设备的 Agent 会话。')
          setScopedSession(null)
          return
        }
        setScopedSession({
          deviceID: requestedDeviceID,
          session: snapshot.session,
          initialView: projectAgentSnapshot(snapshot),
        })
        onSessionChange?.(snapshot.session)
      } catch (nextError) {
        if (!current || routeDeviceID.current !== requestedDeviceID) return
        if (selectedSessionID) {
          setSessionError(nextError instanceof APIError ? nextError.message : '无法恢复该 Agent 会话。')
          setScopedSession(null)
          return
        }
        if (!(nextError instanceof APIError && nextError.status === 404)) {
          setSessionError(nextError instanceof APIError ? nextError.message : '无法恢复 Agent 会话。')
          setScopedSession(null)
          return
        }
        if (!device.online) {
          setScopedSession(null)
          return
        }
        try {
          const created = await api.createAgentSession(requestedDeviceID)
          if (!current || routeDeviceID.current !== requestedDeviceID) return
          if (created.device_id !== requestedDeviceID) {
            setSessionError('服务端返回了其他设备的 Agent 会话。')
            setScopedSession(null)
            return
          }
          setScopedSession({ deviceID: requestedDeviceID, session: created, initialView: initialAgentViewState })
          onSessionChange?.(created)
          onSessionIndexChanged?.()
        } catch (createError) {
          if (!current || routeDeviceID.current !== requestedDeviceID) return
          setSessionError(createError instanceof APIError ? createError.message : '无法新建 Agent 会话。')
          setScopedSession(null)
        }
      }
    })().finally(() => {
      if (current && routeDeviceID.current === requestedDeviceID) setSessionLoading(false)
    })
    return () => { current = false }
  }, [device?.id, device?.online, deviceId, onSessionChange, onSessionIndexChanged, selectedSessionID, sessionReload])

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [state.timeline, state.failure])

  async function createSession(): Promise<boolean> {
    if (!device || !deviceId || device.id !== deviceId) return false
    const requestedDeviceID = device.id
    setCreating(true)
    setSessionError(null)
    try {
      const created = await api.createAgentSession(requestedDeviceID)
      if (routeDeviceID.current !== requestedDeviceID) return false
      if (created.device_id !== requestedDeviceID) {
        setSessionError('服务端返回了其他设备的 Agent 会话。')
        return false
      }
      setScopedSession({ deviceID: requestedDeviceID, session: created, initialView: initialAgentViewState })
      onSessionChange?.(created)
      onSessionIndexChanged?.()
      return true
    } catch (nextError) {
      if (routeDeviceID.current === requestedDeviceID) {
        setSessionError(nextError instanceof APIError ? nextError.message : '无法新建 Agent 会话。')
      }
      return false
    } finally {
      if (routeDeviceID.current === requestedDeviceID) setCreating(false)
    }
  }

  function startNewConversation() {
    void createSession()
  }

  async function sendMessage(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!session || streamState !== 'open' || !prompt.trim() || sending) return
    const content = prompt.trim()
    const requestSessionID = session.id
    const messageID = beginUserMessage(content)
    if (!messageID) return
    setSending(true)
    setSessionError(null)
    setPrompt('')
    try {
      await api.postAgentMessage(requestSessionID, content)
      acceptUserMessage(messageID)
      setCredentialFailureSuppressed(false)
      onSessionIndexChanged?.()
    } catch (nextError) {
      const serverTurnStarted = rejectUserMessage(messageID)
      if (activeSessionID.current === requestSessionID) {
        if (!serverTurnStarted) setPrompt(content)
        if (nextError instanceof APIError && nextError.code === 'PROVIDER_CREDENTIAL_REQUIRED') {
          setCredentialStatus({ configured: false, writable: true })
          setCredentialFailureSuppressed(true)
          setSessionError(null)
        } else {
          setSessionError(nextError instanceof APIError ? nextError.message : '无法发送消息。')
        }
      }
    } finally {
      if (activeSessionID.current === requestSessionID) setSending(false)
    }
  }

  async function updatePermission(mode: ApprovalMode) {
    if (!session || permissionChanging || session.approval_mode === mode) {
      setPermissionOpen(false)
      return
    }
    const sessionID = session.id
    setPermissionChanging(true)
    setPermissionOpen(false)
    setSessionError(null)
    try {
      const updated = await api.updateAgentSessionApproval(sessionID, mode)
      if (activeSessionID.current !== sessionID) return
      setScopedSession((current) => current?.session.id === sessionID
        ? { ...current, session: updated }
        : current)
      onSessionChange?.(updated)
      onSessionIndexChanged?.()
    } catch (nextError) {
      if (activeSessionID.current === sessionID) {
        setSessionError(nextError instanceof APIError ? nextError.message : '无法更新当前会话权限。')
      }
    } finally {
      if (activeSessionID.current === sessionID) setPermissionChanging(false)
    }
  }

  function handleDecision(toolID: string, decision: ToolDecision) {
    markDecision(toolID, decision)
    if (decision === 'approve_session' && session) {
      const expectedSessionID = session.id
      setScopedSession((current) => {
        if (!current || current.deviceID !== deviceId || current.session.id !== expectedSessionID) return current
        return { ...current, session: { ...current.session, approval_mode: 'full_access' } }
      })
    }
  }

  if (loading) return <div className="centered-state">正在准备 Agent…</div>
  if (!device) return <InlineError message={error || '设备不存在。'} />

  return (
    <div className={`agent-page page-stack${embedded ? ' embedded' : ''}`}>
      {!embedded && (
        <div className="page-heading compact agent-surface-toolbar">
          <div><Link className="back-link" to={`/devices/${encodeURIComponent(device.id)}`}>← {device.name}</Link><h1>Agent</h1></div>
          <div className="agent-session-actions">
            {session && (
              <div className="session-chips">
                <span className={`session-chip provider ${provider.runtime}`}>{provider.label}</span>
                <span className="session-chip">{session.approval_mode === 'full_access' ? '完全访问 · 仅当前会话' : '逐条确认命令'}</span>
              </div>
            )}
            <div className="agent-session-buttons">
              {onOpenSettings && (
                <button className="icon-button" type="button" aria-label="打开 Agent 设置" onClick={onOpenSettings}><SettingsIcon /></button>
              )}
              {session && <button className="button ghost small" type="button" disabled={creating} onClick={startNewConversation}>{creating ? '正在创建…' : '新建会话'}</button>}
            </div>
          </div>
        </div>
      )}
      <InlineError message={error} />
      <InlineError message={sessionError} />

      {sessionLoading ? (
        <section className="panel agent-session-loading" role="status">正在恢复最近的 Agent 会话…</section>
      ) : !session ? (
        <>
          {!device.online ? (
            <section className="notice warning" role="alert">设备当前离线，重新连接后将自动创建会话。</section>
          ) : (
            <section className="panel session-setup" aria-labelledby="agent-session-title">
              <p className="eyebrow">Agent 运行时</p>
              <h2 id="agent-session-title">Agent 会话暂不可用</h2>
              <p className="muted">默认逐条确认命令。审批时可仅为当前会话授予完全访问权限。</p>
              <button className="button primary" type="button" onClick={() => setSessionReload((value) => value + 1)}>
                重试
              </button>
            </section>
          )}
        </>
      ) : (
        <section className="agent-workspace">
          {provider.runtime === 'test' && (
            <div className="agent-runtime-bar test" role="status">
              <SparklesIcon /><strong>测试适配器已启用。</strong><span>该会话只验证远程命令链路，不理解自然语言任务。</span>
            </div>
          )}
          {!device.online && <div className="notice warning agent-offline" role="alert">设备当前离线。仍可查看历史记录；重新连接后才能开始新一轮对话。</div>}
          {session.provider === 'dsh' && credentialStatus?.configured === false && (
            <div className="notice warning agent-credential-required" role="alert">
              <div><strong>尚未配置 DeepSeek API 密钥</strong><span>配置后可继续使用当前会话，不需要重新创建。</span></div>
              <button className="button ghost small" type="button" onClick={onOpenSettings}>打开 Agent 设置</button>
            </div>
          )}
          {session.provider === 'dsh' && credentialError && (
            <div className="notice warning agent-credential-required" role="alert">
              <span>{credentialError}</span>
              <button className="button ghost small" type="button" onClick={() => window.dispatchEvent(new Event('aisummoner:dsh-credential-changed'))}>重试</button>
            </div>
          )}
          <div className="conversation" aria-live="polite" aria-label="Agent 对话">
            {hasTools && (
              <div className="conversation-actions">
                <button type="button" onClick={() => setCollapseSignal((value) => value + 1)}>折叠全部命令</button>
              </div>
            )}
            {state.timeline.length === 0 && (
              <div className="conversation-empty">
                <span aria-hidden="true">✦</span>
                <h2>{provider.emptyTitle}</h2>
                <p>{provider.emptyDescription}</p>
              </div>
            )}
            {state.timeline.map((item) => item.kind === 'message' ? (
              <article className={`chat-message ${item.message.role}`} key={item.key}>
                <span className="message-role">{item.message.role === 'user' ? '你' : provider.label}</span>
                <p>{item.message.content}</p>
              </article>
            ) : item.kind === 'reasoning' ? (
              <ReasoningBlock key={item.key} reasoning={item.reasoning} />
            ) : <ToolCallCard key={item.key} tool={item.tool} collapseSignal={collapseSignal} />)}
            {state.failure && !credentialFailureSuppressed &&
              (session.provider !== 'dsh' || credentialStatus?.configured === true) &&
              <div className="notice error" role="alert">{state.failure}</div>}
            {streamState === 'connecting' && <div className="agent-activity" role="status">正在连接 Agent 事件流…</div>}
            {state.turnState === 'running' && <div className="agent-activity" role="status">{provider.workingLabel}</div>}
            {state.turnState === 'waiting' && <div className="agent-activity waiting" role="status">等待命令审批</div>}
            <div ref={endRef} />
          </div>
          {pendingTool ? (
            <ToolApprovalPanel tool={pendingTool} onDecision={handleDecision} />
          ) : (
            <div className="agent-composer-shell">
              <form className="agent-composer" onSubmit={sendMessage}>
                <label className="sr-only" htmlFor="agent-prompt">向 Agent 发送消息</label>
                <textarea
                  id="agent-prompt"
                  value={prompt}
                  onChange={(event) => setPrompt(event.target.value)}
                  placeholder="输入问题，或描述要在这台设备上完成的任务"
                  rows={2}
                  maxLength={16_384}
                  disabled={!device.online || streamState !== 'open' || sending || state.turnState === 'running' || state.turnState === 'waiting' ||
                    (session.provider === 'dsh' && credentialStatus?.configured !== true)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && !event.shiftKey) {
                      event.preventDefault()
                      event.currentTarget.form?.requestSubmit()
                    }
                  }}
                />
                <div className="agent-composer-controls">
                  <button className="composer-runtime" type="button" disabled={!onOpenSettings} onClick={onOpenSettings}>
                    <ModelIcon /><span>{provider.label}</span>
                  </button>
                  <div
                    className="composer-permission-picker"
                    onBlur={(event) => {
                      if (!event.currentTarget.contains(event.relatedTarget)) setPermissionOpen(false)
                    }}
                  >
                    <button
                      className="composer-permission"
                      type="button"
                      aria-haspopup="menu"
                      aria-expanded={permissionOpen}
                      disabled={permissionChanging || sending || state.turnState === 'running' || state.turnState === 'waiting'}
                      onClick={() => setPermissionOpen((value) => !value)}
                    >
                      {permissionChanging ? '正在更新…' : session.approval_mode === 'full_access' ? '完全访问' : '执行命令前询问'}
                      <ChevronDownIcon />
                    </button>
                    {permissionOpen && (
                      <div className="permission-menu" role="menu" aria-label="当前会话权限">
                        <button
                          type="button"
                          role="menuitemradio"
                          aria-checked={session.approval_mode === 'per_command'}
                          onClick={() => void updatePermission('per_command')}
                        >
                          <strong>执行命令前询问</strong><span>每条远程命令都需要确认</span>
                        </button>
                        <button
                          type="button"
                          role="menuitemradio"
                          aria-checked={session.approval_mode === 'full_access'}
                          onClick={() => {
                            setPermissionOpen(false)
                            if (session.approval_mode !== 'full_access') setConfirmFullAccess(true)
                          }}
                        >
                          <strong>完全访问</strong><span>本会话内自动执行远程命令</span>
                        </button>
                      </div>
                    )}
                  </div>
                  <button className="composer-send" type="submit" aria-label="发送" disabled={!device.online || streamState !== 'open' || sending || !prompt.trim() || state.turnState === 'running' || state.turnState === 'waiting' || (session.provider === 'dsh' && credentialStatus?.configured !== true)}>
                    {sending ? <span className="composer-spinner" /> : <SendIcon />}
                  </button>
                </div>
              </form>
            </div>
          )}
        </section>
      )}
      {confirmFullAccess && (
        <ConfirmDialog
          eyebrow="当前会话权限"
          title="允许本会话完全访问被控设备？"
          description={<p>启用后，DSH 在当前会话中发起的远程命令将不再逐条询问。权限只作用于这个会话，可随时切回逐条确认。</p>}
          confirmLabel="启用完全访问"
          busyLabel="正在更新…"
          busy={permissionChanging}
          onCancel={() => setConfirmFullAccess(false)}
          onConfirm={() => {
            setConfirmFullAccess(false)
            void updatePermission('full_access')
          }}
        />
      )}
    </div>
  )
}
