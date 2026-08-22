import { useEffect, useState } from 'react'
import type { AgentReasoningView } from './events'

export function ReasoningBlock({ reasoning }: { reasoning: AgentReasoningView }) {
  const [expanded, setExpanded] = useState(reasoning.running)

  useEffect(() => {
    setExpanded(reasoning.running)
  }, [reasoning.id, reasoning.running])

  return (
    <details
      className={`reasoning-block${reasoning.running ? ' running' : ''}`}
      open={expanded}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary>
        <span className="reasoning-mark" aria-hidden="true">◇</span>
        <strong>{reasoning.running ? 'Thinking…' : 'Think'}</strong>
        <span className="reasoning-toggle">{expanded ? 'Hide' : 'Show'}</span>
      </summary>
      <p>{reasoning.content}</p>
    </details>
  )
}
