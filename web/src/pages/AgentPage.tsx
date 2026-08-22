import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { APIError, api } from '../api/client'
import type { AgentSession } from '../api/types'
import type { ToolDecision } from '../api/types'
import { resolveAgentProviderPresentation } from '../agent/adapters'
import { initialAgentViewState, projectAgentSnapshot } from '../agent/events'
import type { AgentViewState } from '../agent/events'
import { DeepSeekSetupDialog } from '../agent/DeepSeekSetupDialog'
import { ReasoningBlock } from '../agent/ReasoningBlock'
import { ToolApprovalPanel } from '../agent/ToolApprovalPanel'
import { ToolCallCard } from '../agent/ToolCallCard'
import { useAgentEvents } from '../agent/useAgentEvents'
import { InlineError } from '../components/InlineError'
import { useDevice } from '../devices/useDevice'

interface ScopedSession {
  deviceID: string
  session: AgentSession
  initialView: AgentViewState
}

export function AgentPage() {
  const { deviceId } = useParams()
  const { device, loading, error } = useDevice(deviceId)
  const [scopedSession, setScopedSession] = useState<ScopedSession | null>(null)
  const [creating, setCreating] = useState(false)
  const [sessionLoading, setSessionLoading] = useState(true)
  const [sessionReload, setSessionReload] = useState(0)
  const [sessionError, setSessionError] = useState<string | null>(null)
  const [providerNotice, setProviderNotice] = useState<string | null>(null)
  const [showDeepSeekSetup, setShowDeepSeekSetup] = useState(false)
  const [prompt, setPrompt] = useState('')
  const [sending, setSending] = useState(false)
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
    setProviderNotice(null)
    setShowDeepSeekSetup(false)
    setPrompt('')
    setSending(false)
  }, [deviceId])

  useEffect(() => {
    if (!deviceId || !device || device.id !== deviceId) return
    const requestedDeviceID = device.id
    let current = true
    setSessionLoading(true)
    setSessionError(null)
    void (async () => {
      try {
        const snapshot = await api.latestAgentSession(requestedDeviceID)
        if (!current || routeDeviceID.current !== requestedDeviceID) return
        if (snapshot.session.device_id !== requestedDeviceID) {
          setSessionError('The server returned an Agent session for a different device.')
          setScopedSession(null)
          return
        }
        setScopedSession({
          deviceID: requestedDeviceID,
          session: snapshot.session,
          initialView: projectAgentSnapshot(snapshot),
        })
      } catch (nextError) {
        if (!current || routeDeviceID.current !== requestedDeviceID) return
        if (!(nextError instanceof APIError && nextError.status === 404)) {
          setSessionError(nextError instanceof APIError ? nextError.message : 'Could not restore the Agent conversation.')
          setScopedSession(null)
          return
        }
        if (!device.online) {
          setScopedSession(null)
          return
        }
        try {
          const created = await api.createAgentSession(requestedDeviceID, 'per_command')
          if (!current || routeDeviceID.current !== requestedDeviceID) return
          if (created.device_id !== requestedDeviceID) {
            setSessionError('The server returned an Agent session for a different device.')
            setScopedSession(null)
            return
          }
          setScopedSession({ deviceID: requestedDeviceID, session: created, initialView: initialAgentViewState })
        } catch (createError) {
          if (!current || routeDeviceID.current !== requestedDeviceID) return
          setSessionError(createError instanceof APIError ? createError.message : 'Could not start an Agent conversation.')
          setScopedSession(null)
        }
      }
    })().finally(() => {
      if (current && routeDeviceID.current === requestedDeviceID) setSessionLoading(false)
    })
    return () => { current = false }
  }, [device?.id, device?.online, deviceId, sessionReload])

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [state.timeline, state.failure])

  async function createSession(expectedProvider?: string): Promise<boolean> {
    if (!device || !deviceId || device.id !== deviceId) return false
    const requestedDeviceID = device.id
    setCreating(true)
    setSessionError(null)
    try {
      const created = await api.createAgentSession(requestedDeviceID, 'per_command')
      if (routeDeviceID.current !== requestedDeviceID) return false
      if (created.device_id !== requestedDeviceID) {
        setSessionError('The server returned an Agent session for a different device.')
        return false
      }
      if (expectedProvider && created.provider !== expectedProvider) {
        setSessionError('The server did not bind the new conversation to the configured provider.')
        return false
      }
      setScopedSession({ deviceID: requestedDeviceID, session: created, initialView: initialAgentViewState })
      return true
    } catch (nextError) {
      if (routeDeviceID.current === requestedDeviceID) {
        setSessionError(nextError instanceof APIError ? nextError.message : 'Could not start an Agent session.')
      }
      return false
    } finally {
      if (routeDeviceID.current === requestedDeviceID) setCreating(false)
    }
  }

  function startNewConversation() {
    setProviderNotice(null)
    void createSession()
  }

  async function configureDeepSeek(apiKey: string) {
    if (!device) throw new Error('device is unavailable')
    const requestedDeviceID = device.id
    setSessionError(null)
    setProviderNotice(null)
    await api.configureDeepSeek(apiKey)
    if (routeDeviceID.current !== requestedDeviceID) return
    if (!device.online) {
      setProviderNotice('DeepSeek is ready in Server memory. A new conversation can start when this device reconnects.')
      return
    }
    if (await createSession('deepseek')) {
      setProviderNotice('DeepSeek is ready. This new conversation uses the configured key.')
    } else {
      setProviderNotice('DeepSeek is ready in Server memory. Start a new conversation to use it.')
    }
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
    } catch (nextError) {
      const serverTurnStarted = rejectUserMessage(messageID)
      if (activeSessionID.current === requestSessionID) {
        if (!serverTurnStarted) setPrompt(content)
        setSessionError(nextError instanceof APIError ? nextError.message : 'Could not send this message.')
      }
    } finally {
      if (activeSessionID.current === requestSessionID) setSending(false)
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

  if (loading) return <div className="centered-state">Preparing Agent…</div>
  if (!device) return <InlineError message={error || 'Device not found.'} />

  return (
    <div className="agent-page page-stack">
      <div className="page-heading compact">
        <div>
          <Link className="back-link" to={`/devices/${encodeURIComponent(device.id)}`}>← {device.name}</Link>
          <h1>Agent</h1>
        </div>
        <div className="agent-session-actions">
          {session && (
            <div className="session-chips">
              <span className={`session-chip provider ${provider.runtime}`}>{provider.label}</span>
              <span className="session-chip">{session.approval_mode === 'full_access' ? 'Full Access · this session' : 'Confirm commands'}</span>
            </div>
          )}
          <div className="agent-session-buttons">
            <button
              className="button secondary small"
              type="button"
              disabled={sessionLoading || creating || sending || state.turnState === 'running' || state.turnState === 'waiting'}
              onClick={() => {
                setSessionError(null)
                setProviderNotice(null)
                setShowDeepSeekSetup(true)
              }}
            >
              Set up DeepSeek
            </button>
            {session && (
              <button className="button ghost small" type="button" disabled={creating} onClick={startNewConversation}>
                {creating ? 'Starting…' : 'New conversation'}
              </button>
            )}
          </div>
        </div>
      </div>
      <InlineError message={error} />
      <InlineError message={sessionError} />
      {providerNotice && <div className="notice success" role="status">{providerNotice}</div>}

      {sessionLoading ? (
        <section className="panel agent-session-loading" role="status">Restoring the latest Agent conversation…</section>
      ) : !session ? (
        <>
          {!device.online ? (
            <section className="notice warning" role="alert">This device is offline. A conversation will start automatically when it reconnects.</section>
          ) : (
            <section className="panel session-setup" aria-labelledby="agent-session-title">
              <p className="eyebrow">Agent runtime</p>
              <h2 id="agent-session-title">Agent conversation unavailable</h2>
              <p className="muted">The default conversation uses command-by-command approval. You can grant Full Access for only this conversation when approving a command.</p>
              <button className="button primary" type="button" onClick={() => setSessionReload((value) => value + 1)}>
                Try again
              </button>
            </section>
          )}
        </>
      ) : (
        <section className="agent-workspace">
          <div className={`agent-runtime-bar ${provider.runtime}`} role={provider.runtime === 'test' ? 'status' : undefined}>
            {provider.runtime === 'test' ? (
              <><strong>Test adapter active.</strong> This session verifies the remote command path but does not understand natural-language tasks.</>
            ) : (
              <><strong>{provider.label}</strong><span>Remote Agent session</span></>
            )}
          </div>
          {!device.online && <div className="notice warning agent-offline" role="alert">This device is offline. Conversation history remains available; new turns will work after it reconnects.</div>}
          <div className="conversation" aria-live="polite" aria-label="Agent conversation">
            {state.timeline.length === 0 && (
              <div className="conversation-empty">
                <span aria-hidden="true">✦</span>
                <h2>{provider.emptyTitle}</h2>
                <p>{provider.emptyDescription}</p>
              </div>
            )}
            {state.timeline.map((item) => item.kind === 'message' ? (
              <article className={`chat-message ${item.message.role}`} key={item.key}>
                <span className="message-role">{item.message.role === 'user' ? 'You' : provider.label}</span>
                <p>{item.message.content}</p>
              </article>
            ) : item.kind === 'reasoning' ? (
              <ReasoningBlock key={item.key} reasoning={item.reasoning} />
            ) : <ToolCallCard key={item.key} tool={item.tool} />)}
            {state.failure && <div className="notice error" role="alert">{state.failure}</div>}
            {streamState === 'connecting' && <div className="agent-activity" role="status">Connecting Agent event stream…</div>}
            {state.turnState === 'running' && <div className="agent-activity" role="status">{provider.workingLabel}</div>}
            {state.turnState === 'waiting' && <div className="agent-activity waiting" role="status">Waiting for command approval</div>}
            <div ref={endRef} />
          </div>
          {pendingTool ? (
            <ToolApprovalPanel tool={pendingTool} onDecision={handleDecision} />
          ) : (
            <form className="agent-composer" onSubmit={sendMessage}>
              <label className="sr-only" htmlFor="agent-prompt">Message the Agent</label>
              <textarea
                id="agent-prompt"
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                placeholder="Ask the Agent to inspect this device…"
                rows={3}
                maxLength={16_384}
                disabled={!device.online || streamState !== 'open' || sending || state.turnState === 'running' || state.turnState === 'waiting'}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    event.currentTarget.form?.requestSubmit()
                  }
                }}
              />
              <button className="button primary" type="submit" disabled={!device.online || streamState !== 'open' || sending || !prompt.trim() || state.turnState === 'running' || state.turnState === 'waiting'}>
                {sending ? 'Sending…' : 'Send'}
              </button>
            </form>
          )}
        </section>
      )}

      {showDeepSeekSetup && (
        <DeepSeekSetupDialog
          onCancel={() => setShowDeepSeekSetup(false)}
          onConfigure={configureDeepSeek}
        />
      )}

    </div>
  )
}
