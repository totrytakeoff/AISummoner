import { useCallback, useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { APIError, api } from '../api/client'
import type {
  RuntimeProviderDirectory,
  RuntimeProviderModel,
  RuntimeProviderMutation,
  RuntimeProviderProfile,
} from '../api/types'
import { ModelIcon, TrashIcon } from './Icons'

interface RuntimeProvidersSettingsProps {
  onChanged?: () => void
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof APIError ? error.message : fallback
}

function providerState(profile: RuntimeProviderProfile): string {
  if (!profile.active) return profile.configured ? '配置未生效' : '尚未启用'
  if (profile.credential?.configured === false) return '缺少密钥'
  return '可用'
}

function emptyModel(): RuntimeProviderModel {
  return { id: '', name: '' }
}

function normalizeModels(models: RuntimeProviderModel[]): RuntimeProviderModel[] {
  return models.map((model) => ({
    id: model.id.trim(),
    ...(model.name?.trim() ? { name: model.name.trim() } : {}),
    ...(model.context_window ? { context_window: model.context_window } : {}),
    ...(model.max_tokens ? { max_tokens: model.max_tokens } : {}),
  }))
}

function modelsValid(models: RuntimeProviderModel[]): boolean {
  const ids = models.map((model) => model.id.trim())
  return ids.every(Boolean) && new Set(ids).size === ids.length && models.every((model) =>
    (!model.context_window || model.context_window > 0) && (!model.max_tokens || model.max_tokens > 0))
}

interface ModelRowsProps {
  models: RuntimeProviderModel[]
  disabled: boolean
  onChange: (models: RuntimeProviderModel[]) => void
}

function ModelRows({ models, disabled, onChange }: ModelRowsProps) {
  function update(index: number, patch: Partial<RuntimeProviderModel>) {
    onChange(models.map((model, at) => at === index ? { ...model, ...patch } : model))
  }

  return (
    <div className="provider-model-editor">
      {models.map((model, index) => (
        <div className="provider-model-row" key={index}>
          <input
            aria-label={`模型 ${String(index + 1)} ID`}
            value={model.id}
            placeholder="model-id"
            disabled={disabled}
            onChange={(event) => update(index, { id: event.target.value })}
          />
          <input
            aria-label={`模型 ${String(index + 1)} 名称`}
            value={model.name ?? ''}
            placeholder="显示名称（可选）"
            disabled={disabled}
            onChange={(event) => update(index, { name: event.target.value })}
          />
          <input
            aria-label={`模型 ${String(index + 1)} 上下文`}
            type="number"
            min={1}
            value={model.context_window ?? ''}
            placeholder="上下文"
            disabled={disabled}
            onChange={(event) => update(index, {
              context_window: event.target.value ? Number(event.target.value) : undefined,
            })}
          />
          <input
            aria-label={`模型 ${String(index + 1)} 最大输出`}
            type="number"
            min={1}
            value={model.max_tokens ?? ''}
            placeholder="最大输出"
            disabled={disabled}
            onChange={(event) => update(index, {
              max_tokens: event.target.value ? Number(event.target.value) : undefined,
            })}
          />
          <button
            className="icon-button subtle"
            type="button"
            aria-label={`删除模型 ${String(index + 1)}`}
            disabled={disabled}
            onClick={() => onChange(models.filter((_, at) => at !== index))}
          ><TrashIcon /></button>
        </div>
      ))}
      <button className="settings-action provider-add-model" type="button" disabled={disabled} onClick={() => onChange([...models, emptyModel()])}>
        添加模型
      </button>
    </div>
  )
}

interface ProviderEditorProps {
  profile: RuntimeProviderProfile
  protocols: string[]
  writable: boolean
  onCancel: () => void
  onSaved: () => Promise<void>
}

function ProviderEditor({ profile, protocols, writable, onCancel, onSaved }: ProviderEditorProps) {
  const [displayName, setDisplayName] = useState(profile.display_name)
  const [baseURL, setBaseURL] = useState(profile.base_url ?? '')
  const [protocol, setProtocol] = useState(profile.api ?? protocols[0] ?? '')
  const [apiKey, setAPIKey] = useState('')
  const [modelsOverridden, setModelsOverridden] = useState(profile.models_overridden)
  const [models, setModels] = useState<RuntimeProviderModel[]>(profile.models.map((model) => ({ ...model })))
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const keyLocked = profile.credential?.writable === false
  const canSave = writable && !busy && (!modelsOverridden || modelsValid(models))

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSave) return
    setBusy(true)
    setFailure(null)
    const mutation: RuntimeProviderMutation = {
      expected_revision: profile.revision,
      display_name: profile.custom ? displayName.trim() : undefined,
      base_url: baseURL.trim(),
      api: profile.custom ? protocol : undefined,
      models_overridden: modelsOverridden,
      ...(modelsOverridden ? { models: normalizeModels(models) } : {}),
      ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
    }
    try {
      await api.configureRuntimeProvider('dsh', profile.id, mutation)
      setAPIKey('')
      await onSaved()
    } catch (error) {
      setFailure(errorMessage(error, '无法保存供应商配置。'))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    setFailure(null)
    try {
      await api.removeRuntimeProvider('dsh', profile.id, profile.revision)
      await onSaved()
    } catch (error) {
      setFailure(errorMessage(error, '无法删除供应商。'))
    } finally {
      setBusy(false)
      setConfirmRemove(false)
    }
  }

  return (
    <form className="provider-editor" onSubmit={save} autoComplete="off">
      <header className="provider-editor-heading">
        <div><strong>{profile.display_name}</strong><span>{profile.id}</span></div>
        <span className={`settings-status ${profile.active && profile.credential?.configured !== false ? 'ready' : 'missing'}`}>
          {providerState(profile)}
        </span>
      </header>
      <label>
        <span>API 密钥</span>
        <input
          type="password"
          value={apiKey}
          maxLength={4096}
          autoComplete="off"
          spellCheck={false}
          disabled={!writable || busy || keyLocked}
          placeholder={profile.credential?.configured ? '输入新密钥以替换' : keyLocked ? '由只读环境提供' : '留空则使用供应商原生认证'}
          onChange={(event) => setAPIKey(event.target.value)}
        />
      </label>
      <details className="provider-advanced" open={profile.custom}>
        <summary>自定义设置</summary>
        <div className="provider-advanced-body">
          {profile.custom && (
            <label><span>显示名称</span><input value={displayName} disabled={!writable || busy} onChange={(event) => setDisplayName(event.target.value)} /></label>
          )}
          <label>
            <span>API 地址</span>
            <input value={baseURL} disabled={!writable || busy} placeholder="使用 DSH / 供应商默认地址" onChange={(event) => setBaseURL(event.target.value)} />
          </label>
          {profile.custom && (
            <label>
              <span>接口协议</span>
              <select value={protocol} disabled={!writable || busy} onChange={(event) => setProtocol(event.target.value)}>
                {protocols.map((value) => <option value={value} key={value}>{value}</option>)}
              </select>
            </label>
          )}
          <label className="provider-model-toggle">
            <input type="checkbox" checked={modelsOverridden} disabled={!writable || busy} onChange={(event) => setModelsOverridden(event.target.checked)} />
            <span><strong>自定义模型列表</strong><small>关闭后恢复 DSH 内置目录</small></span>
          </label>
          {modelsOverridden && <ModelRows models={models} disabled={!writable || busy} onChange={setModels} />}
        </div>
      </details>
      {failure && <div className="notice error compact" role="alert">{failure}</div>}
      <footer className="provider-editor-actions">
        {profile.removable && (confirmRemove ? (
          <span className="provider-remove-confirm">
            <button className="settings-action danger" type="button" disabled={busy} onClick={() => void remove()}>确认删除</button>
            <button className="settings-action" type="button" disabled={busy} onClick={() => setConfirmRemove(false)}>取消</button>
          </span>
        ) : <button className="settings-action danger" type="button" disabled={busy} onClick={() => setConfirmRemove(true)}><TrashIcon />删除</button>)}
        <span />
        <button className="button ghost small" type="button" disabled={busy} onClick={onCancel}>取消</button>
        <button className="button primary small" type="submit" disabled={!canSave}>{busy ? '正在保存…' : '保存'}</button>
      </footer>
    </form>
  )
}

interface CustomProviderEditorProps {
  directory: RuntimeProviderDirectory
  onCancel: () => void
  onSaved: () => Promise<void>
}

function CustomProviderEditor({ directory, onCancel, onSaved }: CustomProviderEditorProps) {
  const [providerID, setProviderID] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [protocol, setProtocol] = useState(directory.protocols[0] ?? '')
  const [apiKey, setAPIKey] = useState('')
  const [models, setModels] = useState<RuntimeProviderModel[]>([emptyModel()])
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const validID = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(providerID) &&
    !directory.providers.some((profile) => profile.id === providerID)
  const ready = directory.writable && validID && Boolean(baseURL.trim()) && Boolean(protocol) && models.length > 0 && modelsValid(models) && !busy

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    setBusy(true)
    setFailure(null)
    try {
      await api.configureRuntimeProvider('dsh', providerID, {
        expected_revision: directory.custom_provider_revision,
        display_name: displayName.trim(),
        base_url: baseURL.trim(),
        api: protocol,
        models_overridden: true,
        models: normalizeModels(models),
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
      })
      await onSaved()
    } catch (error) {
      setFailure(errorMessage(error, '无法创建自定义供应商。'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="provider-editor" onSubmit={create} autoComplete="off">
      <header className="provider-editor-heading"><div><strong>添加自定义供应商</strong><span>OpenAI / Anthropic 兼容网关或自托管模型</span></div></header>
      <div className="provider-form-grid">
        <label><span>供应商 ID</span><input value={providerID} disabled={busy} placeholder="my-gateway" onChange={(event) => setProviderID(event.target.value)} /></label>
        <label><span>显示名称</span><input value={displayName} disabled={busy} placeholder="可选" onChange={(event) => setDisplayName(event.target.value)} /></label>
        <label className="wide"><span>API 地址</span><input value={baseURL} disabled={busy} placeholder="https://gateway.example/v1" onChange={(event) => setBaseURL(event.target.value)} /></label>
        <label><span>接口协议</span><select value={protocol} disabled={busy} onChange={(event) => setProtocol(event.target.value)}>{directory.protocols.map((value) => <option value={value} key={value}>{value}</option>)}</select></label>
        <label><span>API 密钥</span><input type="password" maxLength={4096} value={apiKey} disabled={busy} autoComplete="off" placeholder="可选" onChange={(event) => setAPIKey(event.target.value)} /></label>
      </div>
      <div className="provider-custom-models"><strong>模型列表</strong><ModelRows models={models} disabled={busy} onChange={setModels} /></div>
      {!validID && providerID && <p className="settings-description error-text">ID 必须以小写字母开头，仅含小写字母、数字和连字符，且不能重复。</p>}
      {failure && <div className="notice error compact" role="alert">{failure}</div>}
      <footer className="provider-editor-actions"><span /><span /><button className="button ghost small" type="button" disabled={busy} onClick={onCancel}>取消</button><button className="button primary small" type="submit" disabled={!ready}>{busy ? '正在创建…' : '创建供应商'}</button></footer>
    </form>
  )
}

export function RuntimeProvidersSettings({ onChanged }: RuntimeProvidersSettingsProps) {
  const [directory, setDirectory] = useState<RuntimeProviderDirectory | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [creatingCustom, setCreatingCustom] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      setDirectory(await api.runtimeProviders('dsh'))
    } catch (nextError) {
      setError(errorMessage(nextError, '无法读取 DSH 供应商配置。'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const visible = useMemo(() => directory?.providers.filter((profile) => profile.active || profile.configured) ?? [], [directory])
  const available = useMemo(() => directory?.providers.filter((profile) => !profile.active && !profile.configured) ?? [], [directory])
  const editingProfile = directory?.providers.find((profile) => profile.id === editing)

  async function changed() {
    setNotice('供应商配置已更新；当前会话无需重建。')
    setEditing(null)
    setCreatingCustom(false)
    await load()
    window.dispatchEvent(new Event('aisummoner:dsh-provider-changed'))
    onChanged?.()
  }

  if (editingProfile && directory) {
    return <ProviderEditor key={editingProfile.id} profile={editingProfile} protocols={directory.protocols} writable={directory.writable} onCancel={() => setEditing(null)} onSaved={changed} />
  }
  if (creatingCustom && directory) {
    return <CustomProviderEditor directory={directory} onCancel={() => setCreatingCustom(false)} onSaved={changed} />
  }

  return (
    <section className="settings-group runtime-provider-settings">
      <div className="settings-group-heading">
        <div><h4>模型供应商</h4><p>配置由 DSH 原生管理；密钥只写入服务端私有凭据库，绝不会返回浏览器。</p></div>
        <button className="settings-action" type="button" disabled={loading} onClick={() => void load()}>{loading ? '正在刷新…' : '刷新'}</button>
      </div>
      {loading && !directory ? <div className="settings-inline-state" role="status">正在读取供应商目录…</div> : null}
      {directory && (
        <div className="runtime-provider-list">
          {visible.map((profile) => (
            <button className="runtime-provider-row" type="button" key={profile.id} onClick={() => { setNotice(null); setEditing(profile.id) }}>
              <span className="runtime-provider-mark"><ModelIcon /></span>
              <span><strong>{profile.display_name}</strong><small>{profile.id}{profile.base_url ? ` · ${profile.base_url}` : ''}</small></span>
              <span className={`settings-status ${profile.active && profile.credential?.configured !== false ? 'ready' : 'missing'}`}>{providerState(profile)}</span>
            </button>
          ))}
          {visible.length === 0 && <div className="settings-empty-state">尚未配置模型供应商。</div>}
        </div>
      )}
      {directory && (
        <div className="provider-add-actions">
          <select
            aria-label="添加内置供应商"
            value=""
            disabled={!directory.writable || available.length === 0}
            onChange={(event) => event.target.value && setEditing(event.target.value)}
          >
            <option value="">添加内置供应商…</option>
            {available.map((profile) => <option value={profile.id} key={profile.id}>{profile.display_name} · {profile.id}</option>)}
          </select>
          <button className="button ghost small" type="button" disabled={!directory.writable} onClick={() => setCreatingCustom(true)}>添加自定义供应商</button>
        </div>
      )}
      {error && <div className="notice error compact" role="alert">{error}</div>}
      {notice && <div className="notice success compact" role="status">{notice}</div>}
    </section>
  )
}
