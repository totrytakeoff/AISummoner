import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SessionRail, sessionGroupLabel } from './SessionRail'
import type { AgentSessionSummary } from '../api/types'

const sessions: AgentSessionSummary[] = [
  {
    id: 'ags_today', device_id: 'dev_one', approval_mode: 'per_command', provider: 'deepseek', state: 'running',
    device_name: 'Remote', archived_at: null,
    title: 'Inspect remote service', created_at: '2026-08-23T09:00:00Z', updated_at: '2026-08-23T10:00:00Z',
  },
  {
    id: 'ags_earlier', device_id: 'dev_one', approval_mode: 'full_access', provider: 'fake', state: 'failed',
    device_name: 'Remote', archived_at: null,
    title: 'Review system logs', created_at: '2026-08-20T09:00:00Z', updated_at: '2026-08-20T10:00:00Z',
  },
]

describe('SessionRail', () => {
  it('groups, searches and selects bounded summaries', async () => {
    const user = userEvent.setup()
    const select = vi.fn()
    render(
      <SessionRail
        sessions={sessions}
        selectedSessionID="ags_today"
        loading={false}
        creating={false}
        online
        error={null}
        mutationError={null}
        collapsed={false}
        onToggleCollapsed={() => {}}
        onSelect={select}
        onArchive={() => {}}
        onDelete={() => {}}
        onCreate={() => {}}
        onRetryLoad={() => {}}
        onDismissMutationError={() => {}}
        now={new Date('2026-08-23T19:00:00+08:00')}
      />,
    )
    expect(screen.getByRole('region', { name: '今天' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '更早' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Inspect remote service,/ })).toHaveAttribute('aria-pressed', 'true')

    await user.type(screen.getByLabelText('搜索会话'), 'logs')
    expect(screen.queryByRole('button', { name: /^Inspect remote service,/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^Review system logs,/ }))
    expect(select).toHaveBeenCalledWith('ags_earlier')
  })

  it('uses deterministic calendar grouping', () => {
    const now = new Date('2026-08-23T19:00:00+08:00')
    expect(sessionGroupLabel('2026-08-23T01:00:00Z', now)).toBe('今天')
    expect(sessionGroupLabel('2026-08-22T15:59:59Z', now)).toBe('更早')
    expect(sessionGroupLabel('invalid', now)).toBe('更早')
  })

  it('keeps valid rows usable while list or creation errors are retryable', async () => {
    const user = userEvent.setup()
    const select = vi.fn()
    const retryList = vi.fn()
    const retryCreate = vi.fn()
    const dismiss = vi.fn()
    render(
      <SessionRail
        sessions={sessions}
        selectedSessionID="ags_today"
        loading={false}
        creating={false}
        online
        error="无法刷新会话。"
        mutationError="无法新建会话。"
        collapsed={false}
        onToggleCollapsed={() => {}}
        onSelect={select}
        onArchive={() => {}}
        onDelete={() => {}}
        onCreate={retryCreate}
        onRetryLoad={retryList}
        onDismissMutationError={dismiss}
      />,
    )
    expect(screen.getByRole('button', { name: /^Inspect remote service,/ })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: /^Inspect remote service,/ }))
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await user.click(screen.getByRole('button', { name: '重试' }))
    await user.click(screen.getByRole('button', { name: '关闭' }))
    expect(select).toHaveBeenCalledWith('ags_today')
    expect(retryList).toHaveBeenCalledOnce()
    expect(retryCreate).toHaveBeenCalledOnce()
    expect(dismiss).toHaveBeenCalledOnce()
  })

  it('keeps archive and delete as independent row actions that never select the conversation', async () => {
    const user = userEvent.setup()
    const select = vi.fn()
    const archive = vi.fn()
    const remove = vi.fn()
    render(
      <SessionRail
        sessions={[sessions[1]!]}
        selectedSessionID={null}
        loading={false}
        creating={false}
        online
        error={null}
        mutationError={null}
        collapsed={false}
        onToggleCollapsed={() => {}}
        onSelect={select}
        onArchive={archive}
        onDelete={remove}
        onCreate={() => {}}
        onRetryLoad={() => {}}
        onDismissMutationError={() => {}}
      />,
    )

    await user.click(screen.getByRole('button', { name: '归档会话：Review system logs' }))
    await user.click(screen.getByRole('button', { name: '删除会话：Review system logs' }))
    expect(archive).toHaveBeenCalledWith(sessions[1])
    expect(remove).toHaveBeenCalledWith(sessions[1])
    expect(select).not.toHaveBeenCalled()
  })
})
