import { Link, useParams } from 'react-router-dom'
import { InlineError } from '../components/InlineError'
import { useDevice } from '../devices/useDevice'
import { TerminalPanel } from '../terminal/TerminalPanel'

export function TerminalPage() {
  const { deviceId } = useParams()
  const { device, loading, error } = useDevice(deviceId)

  if (loading) return <div className="centered-state">正在打开终端…</div>
  if (!device) return <InlineError message={error || '未找到设备。'} />

  return (
    <div className="terminal-page">
      <div className="page-heading compact">
        <div>
          <Link className="back-link" to={`/devices/${encodeURIComponent(device.id)}`}>← {device.name}</Link>
          <h1>终端</h1>
        </div>
        <span className="mono muted">{device.platform}/{device.arch}</span>
      </div>
      <InlineError message={error} />
      {device.online ? (
        <TerminalPanel deviceID={device.id} />
      ) : (
        <section className="notice warning" role="alert">设备当前离线，请在被控客户端重新连接后再试。</section>
      )}
    </div>
  )
}
