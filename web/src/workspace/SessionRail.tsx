import { useMemo, useState } from 'react'
import type { AgentSessionSummary } from '../api/types'
import { resolveAgentProviderPresentation } from '../agent/adapters'
import { AISummonerMark, ArchiveIcon, ChevronLeftIcon, ChevronRightIcon, DeviceIcon, PlusIcon, SearchIcon, SettingsIcon, TrashIcon } from '../components/Icons'

interface SessionRailProps {
  sessions: AgentSessionSummary[]
  selectedSessionID: string | null
  loading: boolean
  creating: boolean
  online: boolean
  error: string | null
  mutationError: string | null
  actionError?: string | null
  collapsed: boolean
  onToggleCollapsed: () => void
  onSelect: (sessionID: string) => void
  onArchive: (session: AgentSessionSummary) => void
  onDelete: (session: AgentSessionSummary) => void
  onCreate: () => void
  onRetryLoad: () => void
  onDismissMutationError: () => void
  onDismissActionError?: () => void
  deviceName?: string
  onBackToDevices?: () => void
  onOpenSettings?: () => void
  mutatingSessionID?: string | null
  now?: Date
}

function validDate(value: string): Date | null {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

export function sessionGroupLabel(updatedAt: string, now = new Date()): '今天' | '更早' {
  const date = validDate(updatedAt)
  if (!date) return '更早'
  return date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
    ? '今天'
    : '更早'
}

function sessionStateLabel(state: string): string {
  switch (state) {
    case 'running': return '处理中'
    case 'waiting_approval': return '等待审批'
    case 'failed': return '失败'
    default: return '就绪'
  }
}

export function SessionRail(props: SessionRailProps) {
  const [query, setQuery] = useState('')
  const groups = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase()
    const visible = normalized
      ? props.sessions.filter((session) => `${session.title} ${session.provider} ${session.state}`.toLocaleLowerCase().includes(normalized))
      : props.sessions
    return ['今天', '更早'].map((label) => ({
      label,
      sessions: visible.filter((session) => sessionGroupLabel(session.updated_at, props.now) === label),
    })).filter((group) => group.sessions.length > 0)
  }, [props.sessions, props.now, query])

  return (
    <div className="session-rail">
      <div className="session-rail-brand">
        <span className="workspace-brand-mark" aria-hidden="true"><AISummonerMark /></span>
        {!props.collapsed && <strong>AISummoner</strong>}
        <button
          className="icon-button session-collapse"
          type="button"
          aria-label={props.collapsed ? '展开会话栏' : '收起会话栏'}
          onClick={props.onToggleCollapsed}
        >
          {props.collapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
        </button>
      </div>
      <button className="rail-device-button" type="button" onClick={props.onBackToDevices} title={props.collapsed ? props.deviceName : undefined}>
        <DeviceIcon />
        {!props.collapsed && <span><strong>{props.deviceName || '设备'}</strong><small>{props.online ? '已连接' : '离线'}</small></span>}
      </button>
      <button
        className="new-session-button"
        type="button"
        aria-label="新建会话"
        disabled={!props.online || props.creating}
        onClick={props.onCreate}
      >
        <PlusIcon />
        {!props.collapsed && <span>{props.creating ? '正在创建…' : '新建会话'}</span>}
      </button>

      {!props.collapsed && (
        <div className="session-search">
          <SearchIcon />
          <label className="sr-only" htmlFor="session-search">搜索会话</label>
          <input
            id="session-search"
            type="search"
            placeholder="搜索会话"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
      )}

      {!props.collapsed && props.error && (
        <div className="session-rail-alert" role="alert">
          <span>{props.error}</span>
          <button type="button" onClick={props.onRetryLoad}>重新加载</button>
        </div>
      )}
      {!props.collapsed && props.mutationError && (
        <div className="session-rail-alert mutation" role="alert">
          <span>{props.mutationError}</span>
          <div>
            <button type="button" disabled={!props.online || props.creating} onClick={props.onCreate}>重试</button>
            <button type="button" onClick={props.onDismissMutationError}>关闭</button>
          </div>
        </div>
      )}
      {!props.collapsed && props.actionError && (
        <div className="session-rail-alert mutation" role="alert">
          <span>{props.actionError}</span>
          <button type="button" onClick={props.onDismissActionError}>关闭</button>
        </div>
      )}
      {props.collapsed && props.error && (
        <button className="icon-button session-error-action" type="button" aria-label={`会话列表错误：${props.error}。重试`} onClick={props.onRetryLoad}>!</button>
      )}
      {props.collapsed && props.mutationError && (
        <button className="icon-button session-error-action" type="button" aria-label={`新建会话错误：${props.mutationError}。重试`} disabled={!props.online || props.creating} onClick={props.onCreate}>!</button>
      )}
      {props.collapsed && props.actionError && (
        <button className="icon-button session-error-action" type="button" aria-label={`会话操作错误：${props.actionError}`} onClick={props.onDismissActionError}>!</button>
      )}

      <div className="session-list" aria-label="最近会话">
        {props.loading && props.sessions.length === 0 ? (
          <div className="session-list-state" role="status">{props.collapsed ? '…' : '正在加载会话…'}</div>
        ) : groups.length === 0 ? (
          <div className="session-list-state">{props.collapsed ? '—' : query ? '没有匹配的会话。' : '还没有会话。'}</div>
        ) : groups.map((group) => (
          <section className="session-group" aria-label={group.label} key={group.label}>
            {!props.collapsed && <h2>{group.label}</h2>}
            {group.sessions.map((session) => {
              const provider = resolveAgentProviderPresentation(session.provider)
              const stateLabel = sessionStateLabel(session.state)
              return (
                <div
                  className="session-row-shell"
                  key={session.id}
                >
                  <button
                    className="session-row"
                    type="button"
                    data-selected={session.id === props.selectedSessionID || undefined}
                    aria-pressed={session.id === props.selectedSessionID}
                    aria-label={`${session.title}, ${provider.label}, ${stateLabel}`}
                    title={props.collapsed ? session.title : undefined}
                    onClick={() => props.onSelect(session.id)}
                  >
                    <span className={`session-state-dot ${session.state}`} aria-hidden="true" />
                    {!props.collapsed && (
                      <span className="session-row-copy">
                        <strong>{session.title}</strong>
                        <small><span>{provider.label}</span><span>{stateLabel}</span></small>
                      </span>
                    )}
                  </button>
                  {!props.collapsed && (
                    <span className="session-row-actions">
                      <button
                        type="button"
                        aria-label={`归档会话：${session.title}`}
                        title="归档会话"
                        disabled={props.mutatingSessionID === session.id || session.state === 'running' || session.state === 'waiting_approval'}
                        onClick={() => props.onArchive(session)}
                      ><ArchiveIcon /></button>
                      <button
                        type="button"
                        aria-label={`删除会话：${session.title}`}
                        title="删除会话"
                        disabled={props.mutatingSessionID === session.id || session.state === 'running' || session.state === 'waiting_approval'}
                        onClick={() => props.onDelete(session)}
                      ><TrashIcon /></button>
                    </span>
                  )}
                </div>
              )
            })}
          </section>
        ))}
      </div>
      <button className="rail-settings-button" type="button" onClick={props.onOpenSettings} aria-label="设置">
        <SettingsIcon />
        {!props.collapsed && <span>设置</span>}
      </button>
    </div>
  )
}
