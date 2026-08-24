import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { APIError, api, normalizePairingCode } from '../api/client'
import type { Device } from '../api/types'
import { InlineError } from '../components/InlineError'
import { StatusBadge } from '../components/StatusBadge'
import { ControllerSettingsDialog } from '../components/ControllerSettingsDialog'
import { ChevronRightIcon, DeviceIcon, PlusIcon, SettingsIcon } from '../components/Icons'
import { useAuth } from '../auth/AuthContext'

function formatSeen(value: string | null): string {
  if (!value) return '从未连接'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

export function DevicesPage() {
  const { user, logout } = useAuth()
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [pairingCode, setPairingCode] = useState('')
  const [pairing, setPairing] = useState(false)
  const [pairError, setPairError] = useState<string | null>(null)
  const [pairSuccess, setPairSuccess] = useState<string | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)

  const refresh = useCallback(async () => {
    try {
      setDevices(await api.devices())
      setLoadError(null)
    } catch (error) {
      setLoadError(error instanceof APIError ? error.message : '无法加载设备列表。')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5_000)
    return () => window.clearInterval(timer)
  }, [refresh])

  async function claim(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const normalized = normalizePairingCode(pairingCode)
    if (normalized.replace('-', '').length !== 8) {
      setPairError('请输入完整的 8 位配对码。')
      return
    }
    setPairing(true)
    setPairError(null)
    setPairSuccess(null)
    try {
      const device = await api.claimPairing(normalized)
      setPairingCode('')
      setPairSuccess(`已成功绑定 ${device.name}。`)
      await refresh()
    } catch (error) {
      setPairError(error instanceof APIError ? error.message : '无法绑定该设备。')
    } finally {
      setPairing(false)
    }
  }

  return (
    <div className="device-hub page-stack">
      <div className="page-heading device-hub-heading">
        <div>
          <h1>设备</h1>
          <p className="muted">选择设备继续操作，或绑定一台新设备。</p>
        </div>
        <div className="device-hub-actions">
          <button className="button ghost" type="button" onClick={() => void refresh()}>刷新</button>
          <button className="icon-button" type="button" aria-label="打开设置" onClick={() => setSettingsOpen(true)}><SettingsIcon /></button>
        </div>
      </div>

      <section className="panel pair-panel" aria-labelledby="pair-title">
        <div className="pair-panel-copy">
          <span className="pair-panel-icon"><PlusIcon /></span>
          <div>
          <h2 id="pair-title">绑定设备</h2>
            <p className="muted">输入 AISummoner 被控端显示的一次性配对码。</p>
          </div>
        </div>
        <form className="pair-form" onSubmit={claim}>
          <label className="sr-only" htmlFor="pairing-code">配对码</label>
          <input
            id="pairing-code"
            name="pairing-code"
            inputMode="text"
            autoComplete="off"
            spellCheck={false}
            placeholder="K7HF-92PQ"
            value={pairingCode}
            onChange={(event) => setPairingCode(normalizePairingCode(event.target.value))}
            aria-describedby="pair-help"
          />
          <button className="button primary" type="submit" disabled={pairing}>
            {pairing ? '正在绑定…' : '绑定设备'}
          </button>
        </form>
        <span id="pair-help" className="muted tiny pair-help">配对码 10 分钟后过期，且只能使用一次。</span>
        <InlineError message={pairError} />
        {pairSuccess && <div className="notice success" role="status">{pairSuccess}</div>}
      </section>

      <InlineError message={loadError} />
      {loading ? (
        <div className="centered-state">正在加载设备…</div>
      ) : devices.length === 0 ? (
        <section className="empty-state">
          <h2>还没有已绑定设备</h2>
          <p>在 Linux 设备上启动 AISummoner 被控端，然后在上方输入配对码。</p>
        </section>
      ) : (
        <section className="device-grid" aria-label="已绑定设备">
          {devices.map((device) => (
            <Link className="device-card" to={`/devices/${encodeURIComponent(device.id)}/workspace`} key={device.id}>
              <span className="device-card-icon"><DeviceIcon /></span>
              <div className="device-card-copy">
                <div className="device-card-title"><h2>{device.name}</h2><StatusBadge online={device.online} /></div>
                <p>{device.platform} / {device.arch} · 客户端 {device.client_version}</p>
                <small>最近在线：{formatSeen(device.last_seen_at)}</small>
              </div>
              <ChevronRightIcon className="device-card-chevron" />
            </Link>
          ))}
        </section>
      )}
      {settingsOpen && (
        <ControllerSettingsDialog
          username={user?.username || '账户'}
          onClose={() => setSettingsOpen(false)}
          onSignOut={logout}
        />
      )}
    </div>
  )
}
