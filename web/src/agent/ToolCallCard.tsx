import { useEffect, useState } from 'react'
import { presentAgentTool } from './adapters'
import type { ToolCallView } from './events'
import { ChevronDownIcon, TerminalIcon } from '../components/Icons'

interface ToolCallCardProps {
  tool: ToolCallView
  collapseSignal?: number
}

const decisionLabels = {
  approve_once: '已单次允许',
  approve_session: '本会话已允许',
  deny: '已拒绝',
} as const

const statusLabels: Record<string, string> = {
  pending: '等待审批',
  approved: '已允许',
  running: '执行中',
  completed: '已完成',
  failed: '失败',
  denied: '已拒绝',
}

const failureLabels: Record<string, string> = {
  COMMAND_DENIED: '命令已被拒绝。',
  DEVICE_OFFLINE: '设备当前离线。',
  REMOTE_EXEC_CANCELED: '远程命令已取消。',
  REMOTE_EXEC_TIMEOUT: '远程命令执行超时。',
  REMOTE_EXEC_TRANSPORT: '远程命令通道异常。',
}

export function ToolCallCard({ tool, collapseSignal = 0 }: ToolCallCardProps) {
  const [expanded, setExpanded] = useState(false)

  useEffect(() => setExpanded(false), [collapseSignal])
  const presentation = presentAgentTool(tool)
  const hasDetails = tool.command !== '(command unavailable)' || Boolean(tool.cwd || tool.timeoutMs || tool.output ||
    tool.exitCode !== undefined || tool.truncated || tool.failureCode || tool.error)
  const status = tool.decision ? decisionLabels[tool.decision] : statusLabels[tool.status] ?? tool.status
  const error = tool.failureCode ? failureLabels[tool.failureCode] ?? '工具调用失败。' : tool.error

  return (
    <article className="tool-card" data-kind={presentation.kind} data-state={tool.status} aria-label={`${tool.name} 工具调用`}>
      <button
        className="tool-summary"
        type="button"
        aria-expanded={expanded}
        disabled={!hasDetails}
        onClick={() => setExpanded((current) => !current)}
      >
        <span className="tool-kind-icon" aria-hidden="true"><TerminalIcon /></span>
        <strong>{presentation.title}</strong>
        <span className="tool-summary-text">{presentation.summary}</span>
        <span className={`tool-status ${tool.status}`}>{status}</span>
        {hasDetails && <span className="tool-chevron" aria-hidden="true"><ChevronDownIcon /></span>}
      </button>
      {expanded && hasDetails && (
        <div className="tool-details">
          <pre className="command-block"><code>{tool.command}</code></pre>
          {(tool.cwd || tool.timeoutMs) && (
            <dl className="tool-meta">
              {tool.cwd && <div><dt>工作目录</dt><dd className="mono">{tool.cwd}</dd></div>}
              {tool.timeoutMs && <div><dt>超时时间</dt><dd>{tool.timeoutMs / 1000} 秒</dd></div>}
            </dl>
          )}
          {tool.output && <pre className="tool-output"><code>{tool.output}</code></pre>}
          {(tool.exitCode !== undefined || tool.truncated || tool.failureCode || tool.error) && (
            <footer>
              {tool.exitCode !== undefined && <span>退出码：<strong>{tool.exitCode}</strong></span>}
              {tool.truncated && <span>输出已截断</span>}
              {tool.failureCode && <span className="mono">{tool.failureCode}</span>}
              {error && <span className="error-text">{error}</span>}
            </footer>
          )}
        </div>
      )}
    </article>
  )
}
