import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { emptyResponse, jsonResponse, renderWithRouter } from '../test/helpers'
import type { ToolDecision } from '../api/types'
import { ToolApprovalPanel } from './ToolApprovalPanel'
import { ToolCallCard } from './ToolCallCard'
import type { ToolCallView } from './events'

const pendingTool: ToolCallView = {
  id: 'tool_1',
  name: 'remote_exec',
  command: 'uname -a',
  status: 'pending',
  output: '',
}

describe('Agent tool presentation and approval', () => {
  it('keeps a tool compact in the ordered conversation and expands its details on demand', async () => {
    renderWithRouter(<ToolCallCard tool={{ ...pendingTool, status: 'completed', output: 'Linux host', exitCode: 0 }} />)

    expect(screen.getByText('uname -a')).toBeInTheDocument()
    expect(screen.queryByText('Linux host')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve once' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Run command/ }))
    expect(screen.getByText('Linux host')).toBeInTheDocument()
    expect(screen.getByText(/Exit code:/)).toHaveTextContent('0')
  })

  it.each<[string, ToolDecision]>([
    ['Approve once', 'approve_once'],
    ['Deny', 'deny'],
  ])('posts %s from the composer approval takeover', async (label, decision) => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(emptyResponse())
    const onDecision = vi.fn()
    renderWithRouter(<ToolApprovalPanel tool={pendingTool} onDecision={onDecision} />)

    await userEvent.click(screen.getByRole('button', { name: label }))
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/tool-calls/tool_1/decision', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ decision }),
    }))
    expect(onDecision).toHaveBeenCalledWith('tool_1', decision)
  })

  it('requires an explicit confirmation before granting conversation-scoped Full Access', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(emptyResponse())
    const onDecision = vi.fn()
    renderWithRouter(<ToolApprovalPanel tool={pendingTool} onDecision={onDecision} />)

    const trigger = screen.getByRole('button', { name: 'Approve session' })
    await user.click(trigger)
    expect(screen.getByRole('alertdialog', { name: 'Approve commands for this conversation?' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    expect(fetchMock).not.toHaveBeenCalled()

    await user.keyboard('{Escape}')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()

    await user.click(trigger)
    await user.click(screen.getByRole('button', { name: 'Approve this conversation' }))
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/tool-calls/tool_1/decision', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ decision: 'approve_session' }),
    }))
    expect(onDecision).toHaveBeenCalledWith('tool_1', 'approve_session')
  })

  it('keeps every composer decision available after an error and succeeds on retry', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({
        error: { code: 'DECISION_CONFLICT', message: 'decision could not be recorded yet' },
      }, 409))
      .mockResolvedValueOnce(emptyResponse())
    const onDecision = vi.fn()
    renderWithRouter(<ToolApprovalPanel tool={pendingTool} onDecision={onDecision} />)

    await user.click(screen.getByRole('button', { name: 'Approve once' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('decision could not be recorded yet')
    expect(screen.getByRole('button', { name: 'Approve once' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Approve session' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Deny' })).toBeEnabled()
    expect(onDecision).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Approve once' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(onDecision).toHaveBeenCalledWith('tool_1', 'approve_once')
  })
})
