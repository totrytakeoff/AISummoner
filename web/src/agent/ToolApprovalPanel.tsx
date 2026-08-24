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
      setError(nextError instanceof APIError ? nextError.message : '无法提交审批结果，请重试。')
      setDeciding(null)
    }
  }

  return (
    <section className="tool-approval-panel" aria-label="命令审批">
      <div className="tool-approval-copy">
        <span className="eyebrow">需要审批</span>
        <strong>允许在被控设备上执行这条命令吗？</strong>
        <code>{tool.command}</code>
      </div>
      <div className="approval-actions">
        <button className="button primary small" type="button" disabled={deciding !== null} onClick={() => void decide('approve_once')}>仅允许本次</button>
        <button className="button secondary small" type="button" disabled={deciding !== null} onClick={() => {
          setError(null)
          setConfirmSessionAccess(true)
        }}>允许当前会话</button>
        <button className="button danger small" type="button" disabled={deciding !== null} onClick={() => void decide('deny')}>拒绝</button>
      </div>
      {error && !confirmSessionAccess && <div className="notice error compact" role="alert">{error}</div>}
      {confirmSessionAccess && (
        <ConfirmDialog
          eyebrow="提升 Agent 权限"
          title="允许当前会话后续执行命令？"
          description={(
            <>
              <p>当前 Agent 会话后续的命令将不再逐条询问。新建会话后该权限自动失效。</p>
              {error && <div className="notice error compact" role="alert">{error}</div>}
            </>
          )}
          confirmLabel="允许当前会话"
          busyLabel="正在批准…"
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
