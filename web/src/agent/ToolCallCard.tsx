import { useState } from 'react'
import { presentAgentTool } from './adapters'
import type { ToolCallView } from './events'

interface ToolCallCardProps {
  tool: ToolCallView
}

const decisionLabels = {
  approve_once: 'Approved once',
  approve_session: 'Approved for session',
  deny: 'Denied',
} as const

export function ToolCallCard({ tool }: ToolCallCardProps) {
  const [expanded, setExpanded] = useState(false)
  const presentation = presentAgentTool(tool)
  const hasDetails = tool.command !== '(command unavailable)' || Boolean(tool.cwd || tool.timeoutMs || tool.output ||
    tool.exitCode !== undefined || tool.truncated || tool.failureCode || tool.error)
  const status = tool.decision ? decisionLabels[tool.decision] : tool.status

  return (
    <article className="tool-card" data-kind={presentation.kind} data-state={tool.status} aria-label={`${tool.name} tool call`}>
      <button
        className="tool-summary"
        type="button"
        aria-expanded={expanded}
        disabled={!hasDetails}
        onClick={() => setExpanded((current) => !current)}
      >
        <span className={`tool-state-dot ${tool.status}`} aria-hidden="true" />
        <strong>{presentation.title}</strong>
        <span className="tool-summary-text">{presentation.summary}</span>
        <span className={`tool-status ${tool.status}`}>{status}</span>
        {hasDetails && <span className="tool-chevron" aria-hidden="true">⌄</span>}
      </button>
      {expanded && hasDetails && (
        <div className="tool-details">
          <pre className="command-block"><code>{tool.command}</code></pre>
          {(tool.cwd || tool.timeoutMs) && (
            <dl className="tool-meta">
              {tool.cwd && <div><dt>Working directory</dt><dd className="mono">{tool.cwd}</dd></div>}
              {tool.timeoutMs && <div><dt>Timeout</dt><dd>{tool.timeoutMs / 1000}s</dd></div>}
            </dl>
          )}
          {tool.output && <pre className="tool-output"><code>{tool.output}</code></pre>}
          {(tool.exitCode !== undefined || tool.truncated || tool.failureCode || tool.error) && (
            <footer>
              {tool.exitCode !== undefined && <span>Exit code: <strong>{tool.exitCode}</strong></span>}
              {tool.truncated && <span>Output truncated</span>}
              {tool.failureCode && <span className="mono">{tool.failureCode}</span>}
              {tool.error && <span className="error-text">{tool.error}</span>}
            </footer>
          )}
        </div>
      )}
    </article>
  )
}
