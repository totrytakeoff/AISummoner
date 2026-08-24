import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { APIError, api } from '../api/client'
import type { AgentSessionSummary, AgentSettings, ApprovalMode, Device, DSHCredentialStatus } from '../api/types'
import { controllerRuntimeDescription, dshAgentExperience } from '../agent/experience'
import { ConfirmDialog } from './ConfirmDialog'
import {
  ArchiveIcon,
  CloseIcon,
  DeviceIcon,
  LogOutIcon,
  ModelIcon,
  RestoreIcon,
  SettingsIcon,
  TrashIcon,
  UserIcon,
} from './Icons'

export type ControllerSettingsSection = 'general' | 'agent' | 'sessions' | 'device'

interface ControllerSettingsDialogProps {
  initialSection?: ControllerSettingsSection
  device?: Device
  runtimeLabel?: string
  username: string
  onClose: () => void
  onConfigureDSH?: (apiKey: string) => Promise<void>
  onUnpair?: () => Promise<void>
  onSignOut: () => Promise<void>
  onArchivedSessionsChanged?: () => void
}

const focusableSelector = [
  'button:not([disabled])',
  'input:not([disabled])',
  '[href]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function formatTime(value: string | null | undefined): string {
  if (!value) return '从未'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

const allSections: Array<{ id: ControllerSettingsSection; label: string; icon: typeof SettingsIcon }> = [
  { id: 'general', label: '通用', icon: SettingsIcon },
  { id: 'agent', label: 'Agent 与模型', icon: ModelIcon },
  { id: 'sessions', label: '会话管理', icon: ArchiveIcon },
  { id: 'device', label: '设备', icon: DeviceIcon },
]

export function ControllerSettingsDialog({
  initialSection = 'general',
  device,
  runtimeLabel = 'DeepSeek Harness',
  username,
  onClose,
  onConfigureDSH = (apiKey) => api.configureDSH(apiKey),
  onUnpair,
  onSignOut,
  onArchivedSessionsChanged,
}: ControllerSettingsDialogProps) {
  const hasDevice = Boolean(device)
  const sections = useMemo(() => allSections.filter((item) => item.id !== 'device' || hasDevice), [hasDevice])
  const safeInitialSection = initialSection === 'device' && !hasDevice ? 'general' : initialSection
  const [section, setSection] = useState<ControllerSettingsSection>(safeInitialSection)
  const [apiKey, setAPIKey] = useState('')
  const [configuring, setConfiguring] = useState(false)
  const [providerStatus, setProviderStatus] = useState<DSHCredentialStatus | null>(null)
  const [providerLoading, setProviderLoading] = useState(true)
  const [providerError, setProviderError] = useState<string | null>(null)
  const [providerNotice, setProviderNotice] = useState<string | null>(null)
  const [settings, setSettings] = useState<AgentSettings | null>(null)
  const [settingsLoading, setSettingsLoading] = useState(true)
  const [settingsSaving, setSettingsSaving] = useState(false)
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [confirmDefaultFullAccess, setConfirmDefaultFullAccess] = useState(false)
  const [archivedSessions, setArchivedSessions] = useState<AgentSessionSummary[]>([])
  const [archivedLoading, setArchivedLoading] = useState(false)
  const [archivedLoaded, setArchivedLoaded] = useState(false)
  const [archivedError, setArchivedError] = useState<string | null>(null)
  const [mutatingSessionID, setMutatingSessionID] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AgentSessionSummary | null>(null)
  const [confirmingUnpair, setConfirmingUnpair] = useState(false)
  const [unpairing, setUnpairing] = useState(false)
  const [unpairError, setUnpairError] = useState<string | null>(null)
  const [signingOut, setSigningOut] = useState(false)
  const dialogRef = useRef<HTMLElement>(null)
  const closeHandler = useRef(onClose)
  const busy = useRef(false)
  const nestedConfirmation = useRef(false)
  const titleID = useId()

  closeHandler.current = onClose
  busy.current = configuring || settingsSaving || Boolean(mutatingSessionID) || unpairing || signingOut
  nestedConfirmation.current = confirmingUnpair || confirmDefaultFullAccess || Boolean(deleteTarget)

  useEffect(() => {
    setSection(initialSection === 'device' && !hasDevice ? 'general' : initialSection)
  }, [hasDevice, initialSection])

  useEffect(() => {
    let current = true
    void api.dshCredentialStatus().then((status) => {
      if (!current) return
      setProviderStatus(status)
      setProviderError(null)
    }).catch((nextError) => {
      if (!current) return
      setProviderError(nextError instanceof APIError ? nextError.message : '无法读取 DSH 凭据状态。')
    }).finally(() => {
      if (current) setProviderLoading(false)
    })
    void api.agentSettings().then((value) => {
      if (!current) return
      setSettings(value)
      setSettingsError(null)
    }).catch((nextError) => {
      if (!current) return
      setSettingsError(nextError instanceof APIError ? nextError.message : '无法读取 Agent 设置。')
    }).finally(() => {
      if (current) setSettingsLoading(false)
    })
    return () => { current = false }
  }, [])

  async function loadArchivedSessions() {
    setArchivedLoading(true)
    setArchivedError(null)
    try {
      setArchivedSessions(await api.archivedAgentSessions())
      setArchivedLoaded(true)
    } catch (nextError) {
      setArchivedError(nextError instanceof APIError ? nextError.message : '无法读取已归档会话。')
    } finally {
      setArchivedLoaded(true)
      setArchivedLoading(false)
    }
  }

  useEffect(() => {
    if (section === 'sessions' && !archivedLoaded && !archivedLoading) void loadArchivedSessions()
  }, [archivedLoaded, archivedLoading, section])

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialogRef.current?.focus({ preventScroll: true })

    function keyDown(event: KeyboardEvent) {
      const dialog = dialogRef.current
      if (!dialog) return
      if (event.key === 'Escape') {
        if (busy.current || nestedConfirmation.current) return
        event.preventDefault()
        closeHandler.current()
        return
      }
      if (event.key !== 'Tab') return
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector))
      if (focusable.length === 0) return
      const first = focusable[0]!
      const last = focusable.at(-1)!
      if (event.shiftKey && (document.activeElement === first || !dialog.contains(document.activeElement))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (document.activeElement === last || !dialog.contains(document.activeElement))) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', keyDown, true)
    return () => {
      document.removeEventListener('keydown', keyDown, true)
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
    }
  }, [])

  async function configure(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = apiKey.trim()
    if (!key || configuring) return
    setConfiguring(true)
    setProviderError(null)
    setProviderNotice(null)
    try {
      await onConfigureDSH(key)
      const status = await api.dshCredentialStatus()
      setProviderStatus(status)
      setAPIKey('')
      setProviderNotice('密钥已保存。当前会话和旧会话都可以立即继续使用。')
      window.dispatchEvent(new Event('aisummoner:dsh-credential-changed'))
    } catch (nextError) {
      setProviderError(nextError instanceof APIError ? nextError.message : '无法配置 DSH 运行时。')
    } finally {
      setConfiguring(false)
    }
  }

  async function saveDefaultPermission(mode: ApprovalMode) {
    if (settingsSaving || settings?.default_approval_mode === mode) {
      setConfirmDefaultFullAccess(false)
      return
    }
    setSettingsSaving(true)
    setSettingsError(null)
    try {
      setSettings(await api.updateAgentSettings(mode))
      setConfirmDefaultFullAccess(false)
    } catch (nextError) {
      setSettingsError(nextError instanceof APIError ? nextError.message : '无法保存默认权限。')
    } finally {
      setSettingsSaving(false)
    }
  }

  async function restoreSession(session: AgentSessionSummary) {
    if (mutatingSessionID) return
    setMutatingSessionID(session.id)
    setArchivedError(null)
    try {
      await api.setAgentSessionArchived(session.id, false)
      setArchivedSessions((current) => current.filter((item) => item.id !== session.id))
      onArchivedSessionsChanged?.()
    } catch (nextError) {
      setArchivedError(nextError instanceof APIError ? nextError.message : '无法恢复会话。')
    } finally {
      setMutatingSessionID(null)
    }
  }

  async function deleteSession(session: AgentSessionSummary) {
    if (mutatingSessionID) return
    setMutatingSessionID(session.id)
    setArchivedError(null)
    try {
      await api.deleteAgentSession(session.id)
      setArchivedSessions((current) => current.filter((item) => item.id !== session.id))
      setDeleteTarget(null)
      onArchivedSessionsChanged?.()
    } catch (nextError) {
      setArchivedError(nextError instanceof APIError ? nextError.message : '无法删除会话。')
    } finally {
      setMutatingSessionID(null)
    }
  }

  async function unpair() {
    if (!onUnpair) return
    setUnpairing(true)
    setUnpairError(null)
    try {
      await onUnpair()
    } catch (nextError) {
      setUnpairError(nextError instanceof APIError ? nextError.message : '无法解除该设备的绑定。')
      setUnpairing(false)
      setConfirmingUnpair(false)
    }
  }

  async function signOut() {
    setSigningOut(true)
    try {
      await onSignOut()
    } finally {
      setSigningOut(false)
    }
  }

  const sectionDescription = section === 'general'
    ? '控制端外观与账户'
    : section === 'agent'
      ? '运行时、凭据和未来会话的默认权限'
      : section === 'sessions'
        ? '恢复或永久删除已归档会话'
        : '连接信息与设备归属'

  return (
    <>
      <div className="controller-settings-overlay">
        <button className="controller-settings-mask" type="button" aria-label="关闭设置" onClick={() => !busy.current && onClose()} />
        <section
          ref={dialogRef}
          className="controller-settings"
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleID}
          tabIndex={-1}
        >
          <nav className="settings-nav" aria-label="设置分类">
            <h2 id={titleID}>设置</h2>
            <div className="settings-nav-list">
              {sections.map((item) => {
                const Icon = item.icon
                return (
                  <button
                    type="button"
                    key={item.id}
                    className="settings-nav-item"
                    data-active={section === item.id || undefined}
                    aria-current={section === item.id ? 'page' : undefined}
                    onClick={() => setSection(item.id)}
                  >
                    <Icon />
                    <span>{item.label}</span>
                  </button>
                )
              })}
            </div>
            <div className="settings-account">
              <UserIcon />
              <span><strong>{username}</strong><small>已登录</small></span>
            </div>
          </nav>

          <div className="settings-content">
            <header className="settings-header">
              <div>
                <h3>{sections.find((item) => item.id === section)?.label}</h3>
                <p>{sectionDescription}</p>
              </div>
              <button className="icon-button" type="button" aria-label="关闭设置" disabled={busy.current} onClick={onClose}>
                <CloseIcon />
              </button>
            </header>

            <div className="settings-options">
              {section === 'general' && (
                <div className="settings-section-stack">
                  <section className="settings-group">
                    <h4>外观</h4>
                    <div className="settings-row">
                      <div><strong>界面风格</strong><p>轻量、克制，并以内容为中心。</p></div>
                      <span className="settings-value">DSH Light</span>
                    </div>
                    <div className="settings-row">
                      <div><strong>控制端体验</strong><p>与锁定版本的 DSH 交互模型保持一致。</p></div>
                      <span className="settings-value">{dshAgentExperience.label}</span>
                    </div>
                  </section>
                  <section className="settings-group">
                    <h4>账户</h4>
                    <div className="settings-row">
                      <div><strong>{username}</strong><p>退出登录不会断开被控设备。</p></div>
                      <button className="settings-action" type="button" disabled={signingOut} onClick={() => void signOut()}>
                        <LogOutIcon />{signingOut ? '正在退出…' : '退出登录'}
                      </button>
                    </div>
                  </section>
                </div>
              )}

              {section === 'agent' && (
                <div className="settings-section-stack">
                  <section className="settings-hero-card">
                    <span className="settings-hero-icon"><ModelIcon /></span>
                    <div>
                      <span className="settings-kicker">一等体验层</span>
                      <h4>{dshAgentExperience.label}</h4>
                      <p>{controllerRuntimeDescription(runtimeLabel)}。体验层与运行时保持分离，后续适配器复用同一套会话界面。</p>
                    </div>
                  </section>
                  <section className="settings-group">
                    <div className="settings-group-heading">
                      <div><h4>DSH 运行时</h4><p>密钥只写入服务端私有 DSH 凭据库，绝不会返回浏览器。</p></div>
                      <span className={`settings-status ${providerStatus?.configured ? 'ready' : 'missing'}`}>
                        {providerLoading ? '正在检查…' : providerStatus?.configured ? '已配置' : '需要配置'}
                      </span>
                    </div>
                    <form className="settings-provider-form" onSubmit={configure} autoComplete="off">
                      <label htmlFor="settings-dsh-key">DeepSeek API 密钥</label>
                      <div className="settings-inline-form">
                        <input
                          id="settings-dsh-key"
                          type="password"
                          autoComplete="off"
                          spellCheck={false}
                          maxLength={4096}
                          placeholder={providerStatus?.configured ? '输入新密钥以替换' : 'sk-…'}
                          value={apiKey}
                          onChange={(event) => setAPIKey(event.target.value)}
                        />
                        <button className="button primary" type="submit" disabled={configuring || !apiKey.trim() || providerStatus?.writable === false}>
                          {configuring ? '正在保存…' : providerStatus?.configured ? '替换密钥' : '保存密钥'}
                        </button>
                      </div>
                      {providerStatus?.writable === false && <p className="settings-description">当前密钥由只读运行环境提供，无法从控制端替换。</p>}
                      {providerError && <div className="notice error compact" role="alert">{providerError}</div>}
                      {providerNotice && <div className="notice success compact" role="status">{providerNotice}</div>}
                    </form>
                  </section>
                  <section className="settings-group">
                    <h4>新会话默认权限</h4>
                    <p className="settings-description">只影响以后创建的会话；当前会话在输入框旁单独设置。</p>
                    {settingsLoading ? <div className="settings-inline-state" role="status">正在读取默认权限…</div> : (
                      <div className="permission-choice-list" role="radiogroup" aria-label="新会话默认权限">
                        <button
                          type="button"
                          role="radio"
                          aria-checked={settings?.default_approval_mode === 'per_command'}
                          data-selected={settings?.default_approval_mode === 'per_command' || undefined}
                          disabled={settingsSaving}
                          onClick={() => void saveDefaultPermission('per_command')}
                        ><strong>执行命令前询问</strong><span>每条远程命令都需要确认</span></button>
                        <button
                          type="button"
                          role="radio"
                          aria-checked={settings?.default_approval_mode === 'full_access'}
                          data-selected={settings?.default_approval_mode === 'full_access' || undefined}
                          disabled={settingsSaving}
                          onClick={() => settings?.default_approval_mode !== 'full_access' && setConfirmDefaultFullAccess(true)}
                        ><strong>完全访问</strong><span>新会话自动执行远程命令</span></button>
                      </div>
                    )}
                    {settingsError && <div className="notice error compact" role="alert">{settingsError}</div>}
                  </section>
                  <section className="settings-group settings-roadmap">
                    <h4>运行时适配器</h4>
                    <div className="runtime-roadmap-row"><strong>DSH</strong><span>一等运行时 · 已接入</span></div>
                    <div className="runtime-roadmap-row"><strong>OpenCode</strong><span>已有适配器 · 待升级完整会话能力</span></div>
                    <div className="runtime-roadmap-row"><strong>Codex</strong><span>计划中</span></div>
                    <div className="runtime-roadmap-row"><strong>Claude Code</strong><span>计划中</span></div>
                  </section>
                </div>
              )}

              {section === 'sessions' && (
                <div className="settings-section-stack">
                  <section className="settings-group settings-session-management">
                    <div className="settings-group-heading">
                      <div><h4>已归档会话</h4><p>归档只从工作区隐藏会话；恢复后仍保留原对话和 DSH 上下文。</p></div>
                      <button className="settings-action" type="button" disabled={archivedLoading} onClick={() => void loadArchivedSessions()}>刷新</button>
                    </div>
                    {archivedLoading && archivedSessions.length === 0 ? (
                      <div className="settings-inline-state" role="status">正在加载已归档会话…</div>
                    ) : archivedSessions.length === 0 ? (
                      <div className="settings-empty-state">没有已归档会话。</div>
                    ) : (
                      <div className="archived-session-list">
                        {archivedSessions.map((session) => (
                          <article className="archived-session-row" key={session.id}>
                            <div>
                              <strong>{session.title}</strong>
                              <span>{session.device_name || session.device_id} · {formatTime(session.archived_at)}</span>
                            </div>
                            <div>
                              <button className="settings-action" type="button" aria-label={`恢复会话：${session.title}`} disabled={Boolean(mutatingSessionID)} onClick={() => void restoreSession(session)}>
                                <RestoreIcon />{mutatingSessionID === session.id ? '正在处理…' : '恢复'}
                              </button>
                              <button className="settings-action danger" type="button" aria-label={`删除会话：${session.title}`} disabled={Boolean(mutatingSessionID)} onClick={() => setDeleteTarget(session)}>
                                <TrashIcon />删除
                              </button>
                            </div>
                          </article>
                        ))}
                      </div>
                    )}
                    {archivedError && <div className="notice error compact" role="alert">{archivedError}</div>}
                  </section>
                </div>
              )}

              {section === 'device' && device && (
                <div className="settings-section-stack">
                  <section className="settings-device-heading">
                    <span className={`settings-device-dot ${device.online ? 'online' : 'offline'}`} />
                    <div><h4>{device.name}</h4><p>{device.online ? '已连接' : '离线'} · {device.platform} / {device.arch}</p></div>
                  </section>
                  <section className="settings-group">
                    <h4>设备详情</h4>
                    <dl className="settings-metadata">
                      <div><dt>设备 ID</dt><dd className="mono">{device.id}</dd></div>
                      <div><dt>客户端版本</dt><dd>{device.client_version}</dd></div>
                      <div><dt>绑定时间</dt><dd>{formatTime(device.paired_at)}</dd></div>
                      <div><dt>最近在线</dt><dd>{formatTime(device.last_seen_at)}</dd></div>
                    </dl>
                  </section>
                  <section className="settings-group settings-danger-zone">
                    <div><h4>解除设备绑定</h4><p>关闭活跃的终端和 Agent 会话，并要求设备重新生成配对码。</p></div>
                    <button className="settings-action danger" type="button" disabled={!onUnpair} onClick={() => setConfirmingUnpair(true)}>
                      <TrashIcon />解除绑定
                    </button>
                    {unpairError && <div className="notice error compact" role="alert">{unpairError}</div>}
                  </section>
                </div>
              )}
            </div>
          </div>
        </section>
      </div>

      {confirmDefaultFullAccess && (
        <ConfirmDialog
          eyebrow="新会话默认权限"
          title="将完全访问设为默认？"
          description={<p>以后创建的会话将自动执行 DSH 发起的远程命令，不再逐条询问。现有会话不会改变。</p>}
          confirmLabel="设为默认"
          busyLabel="正在保存…"
          busy={settingsSaving}
          onCancel={() => !settingsSaving && setConfirmDefaultFullAccess(false)}
          onConfirm={() => void saveDefaultPermission('full_access')}
        />
      )}

      {deleteTarget && (
        <ConfirmDialog
          eyebrow="删除归档会话"
          title={`永久删除“${deleteTarget.title}”？`}
          description={<p>该会话在 AISummoner 中保存的消息和命令记录将一并删除，无法恢复。</p>}
          confirmLabel="永久删除"
          busyLabel="正在删除…"
          busy={mutatingSessionID === deleteTarget.id}
          onCancel={() => !mutatingSessionID && setDeleteTarget(null)}
          onConfirm={() => void deleteSession(deleteTarget)}
        />
      )}

      {confirmingUnpair && device && (
        <ConfirmDialog
          eyebrow="设备归属"
          title={`解除 ${device.name} 的绑定？`}
          description={<p>所有活跃控制会话都将关闭。该设备必须重新提供配对码后才能再次受控。</p>}
          confirmLabel="确认解除绑定"
          busyLabel="正在解除…"
          busy={unpairing}
          onCancel={() => {
            if (!unpairing) setConfirmingUnpair(false)
          }}
          onConfirm={() => void unpair()}
        />
      )}
    </>
  )
}
