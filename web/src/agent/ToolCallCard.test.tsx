import { screen, within } from '@testing-library/react'
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
    expect(screen.queryByRole('button', { name: '仅允许本次' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /运行命令/ }))
    expect(screen.getByText('Linux host')).toBeInTheDocument()
    expect(screen.getByText(/退出码：/)).toHaveTextContent('0')
  })

  it('collapses every expanded command card when the shared collapse signal changes', async () => {
    const first = { ...pendingTool, id: 'tool_first', status: 'completed', output: 'first output', exitCode: 0 }
    const second = { ...pendingTool, id: 'tool_second', command: 'hostname', status: 'completed', output: 'second output', exitCode: 0 }
    const view = renderWithRouter(<><ToolCallCard tool={first} collapseSignal={0} /><ToolCallCard tool={second} collapseSignal={0} /></>)

    for (const trigger of screen.getAllByRole('button', { name: /运行命令/ })) await userEvent.click(trigger)
    expect(screen.getByText('first output')).toBeInTheDocument()
    expect(screen.getByText('second output')).toBeInTheDocument()

    view.rerender(<><ToolCallCard tool={first} collapseSignal={1} /><ToolCallCard tool={second} collapseSignal={1} /></>)
    expect(screen.queryByText('first output')).not.toBeInTheDocument()
    expect(screen.queryByText('second output')).not.toBeInTheDocument()
  })

  it.each<[string, ToolDecision]>([
    ['仅允许本次', 'approve_once'],
    ['拒绝', 'deny'],
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

    const trigger = screen.getByRole('button', { name: '允许当前会话' })
    await user.click(trigger)
    expect(screen.getByRole('alertdialog', { name: '允许当前会话后续执行命令？' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消' })).toHaveFocus()
    expect(fetchMock).not.toHaveBeenCalled()

    await user.keyboard('{Escape}')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()

    await user.click(trigger)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '允许当前会话' }))
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

    await user.click(screen.getByRole('button', { name: '仅允许本次' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('请求未能完成，请检查后重试。')
    expect(screen.getByRole('button', { name: '仅允许本次' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '允许当前会话' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '拒绝' })).toBeEnabled()
    expect(onDecision).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: '仅允许本次' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(onDecision).toHaveBeenCalledWith('tool_1', 'approve_once')
  })
})
