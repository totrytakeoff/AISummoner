import { useEffect, useState } from 'react'
import type { AgentReasoningView } from './events'
import { ChevronDownIcon, SparklesIcon } from '../components/Icons'

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
        <span className="reasoning-mark"><SparklesIcon /></span>
        <strong>{reasoning.running ? '正在思考…' : '思考过程'}</strong>
        <span className="reasoning-toggle"><ChevronDownIcon /></span>
      </summary>
      <p>{reasoning.content}</p>
    </details>
  )
}
