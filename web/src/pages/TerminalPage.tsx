import { Link, useParams } from 'react-router-dom'
import { InlineError } from '../components/InlineError'
import { useDevice } from '../devices/useDevice'
import { TerminalPanel } from '../terminal/TerminalPanel'

export function TerminalPage() {
  const { deviceId } = useParams()
  const { device, loading, error } = useDevice(deviceId)

  if (loading) return <div className="centered-state">Opening terminal…</div>
  if (!device) return <InlineError message={error || 'Device not found.'} />

  return (
    <div className="terminal-page">
      <div className="page-heading compact">
        <div>
          <Link className="back-link" to={`/devices/${encodeURIComponent(device.id)}`}>← {device.name}</Link>
          <h1>Terminal</h1>
        </div>
        <span className="mono muted">{device.platform}/{device.arch}</span>
      </div>
      <InlineError message={error} />
      {device.online ? (
        <TerminalPanel deviceID={device.id} />
      ) : (
        <section className="notice warning" role="alert">This device is offline. Return when the Remote Client reconnects.</section>
      )}
    </div>
  )
}
