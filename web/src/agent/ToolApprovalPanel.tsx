import { useState } from 'react'
import { APIError, api } from '../api/client'
import type { ToolDecision } from '../api/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { ToolCallView } from './events'

interface ToolApprovalPanelProps {
  tool: ToolCallView
  onDecision: (toolID: string, decision: ToolDecision) => void
}

export function ToolApprovalPanel({ tool, onDecision }: ToolApprovalPanelProps) {
  const [deciding, setDeciding] = useState<ToolDecision | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [confirmSessionAccess, setConfirmSessionAccess] = useState(false)

  async function decide(decision: ToolDecision) {
    setDeciding(decision)
    setError(null)
    try {
      await api.decideToolCall(tool.id, decision)
      setConfirmSessionAccess(false)
      onDecision(tool.id, decision)
    } catch (nextError) {
      setError(nextError instanceof APIError ? nextError.message : 'Could not send this decision. Try again.')
      setDeciding(null)
    }
  }

  return (
    <section className="tool-approval-panel" aria-label="Command approval">
      <div className="tool-approval-copy">
        <span className="eyebrow">Approval required</span>
        <strong>Allow this command on the remote device?</strong>
        <code>{tool.command}</code>
      </div>
      <div className="approval-actions">
        <button className="button primary small" type="button" disabled={deciding !== null} onClick={() => void decide('approve_once')}>Approve once</button>
        <button className="button secondary small" type="button" disabled={deciding !== null} onClick={() => {
          setError(null)
          setConfirmSessionAccess(true)
        }}>Approve session</button>
        <button className="button danger small" type="button" disabled={deciding !== null} onClick={() => void decide('deny')}>Deny</button>
      </div>
      {error && !confirmSessionAccess && <div className="notice error compact" role="alert">{error}</div>}
      {confirmSessionAccess && (
        <ConfirmDialog
          eyebrow="Elevated Agent access"
          title="Approve commands for this conversation?"
          description={(
            <>
              <p>Future commands in this Agent conversation will run without asking again. This permission ends when you start a new conversation.</p>
              {error && <div className="notice error compact" role="alert">{error}</div>}
            </>
          )}
          confirmLabel="Approve this conversation"
          busyLabel="Approving…"
          busy={deciding === 'approve_session'}
          onCancel={() => {
            setConfirmSessionAccess(false)
            setError(null)
          }}
          onConfirm={() => void decide('approve_session')}
        />
      )}
    </section>
  )
}
