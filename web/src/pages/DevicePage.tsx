import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { APIError, api } from '../api/client'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { InlineError } from '../components/InlineError'
import { StatusBadge } from '../components/StatusBadge'
import { useDevice } from '../devices/useDevice'

function displayTime(value: string | null): string {
  if (!value) return 'Never'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

export function DevicePage() {
  const { deviceId } = useParams()
  const navigate = useNavigate()
  const { device, loading, error, refresh } = useDevice(deviceId)
  const [confirming, setConfirming] = useState(false)
  const [unpairing, setUnpairing] = useState(false)
  const [unpairError, setUnpairError] = useState<string | null>(null)

  async function unpair() {
    if (!device) return
    setUnpairing(true)
    setUnpairError(null)
    try {
      await api.unpair(device.id)
      navigate('/devices', { replace: true })
    } catch (nextError) {
      setUnpairError(nextError instanceof APIError ? nextError.message : 'Could not unpair this device.')
      setUnpairing(false)
    }
  }

  if (loading) return <div className="centered-state">Loading device…</div>
  if (!device) return <InlineError message={error || 'Device not found.'} />

  return (
    <div className="page-stack">
      <Link className="back-link" to="/devices">← All devices</Link>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Remote device</p>
          <h1>{device.name}</h1>
        </div>
        <StatusBadge online={device.online} />
      </div>
      <InlineError message={error} />

      <section className="panel device-overview">
        <dl className="metadata-grid wide">
          <div><dt>Device ID</dt><dd className="mono">{device.id}</dd></div>
          <div><dt>Operating system</dt><dd>{device.platform}</dd></div>
          <div><dt>Architecture</dt><dd>{device.arch}</dd></div>
          <div><dt>Client version</dt><dd>{device.client_version}</dd></div>
          <div><dt>Last seen</dt><dd>{displayTime(device.last_seen_at)}</dd></div>
          <div><dt>Paired</dt><dd>{displayTime(device.paired_at)}</dd></div>
        </dl>
      </section>

      {device.online ? (
        <section className="action-grid" aria-label="Device actions">
          <Link className="action-card" to={`/devices/${encodeURIComponent(device.id)}/terminal`}>
            <span className="action-icon" aria-hidden="true">›_</span>
            <span><strong>Open terminal</strong><small>Interactive SSH shell in your browser</small></span>
          </Link>
          <Link className="action-card" to={`/devices/${encodeURIComponent(device.id)}/agent`}>
            <span className="action-icon" aria-hidden="true">✦</span>
            <span><strong>Summon Agent</strong><small>Ask OpenCode to inspect this device</small></span>
          </Link>
        </section>
      ) : (
        <section className="notice warning" role="status">
          Terminal and Agent actions are unavailable while this device is offline.
        </section>
      )}

      <section className="danger-zone" aria-labelledby="danger-title">
        <div>
          <h2 id="danger-title">Unpair device</h2>
          <p className="muted">Existing Terminal and Agent sessions will be closed.</p>
        </div>
        <button className="button danger" type="button" onClick={() => setConfirming(true)}>
          Unpair
        </button>
      </section>
      <InlineError message={unpairError} />

      {confirming && (
        <ConfirmDialog
          title={`Unpair ${device.name}?`}
          description={<p>The device must present a new pairing code before you can control it again.</p>}
          confirmLabel="Unpair device"
          busyLabel="Unpairing…"
          busy={unpairing}
          onCancel={() => setConfirming(false)}
          onConfirm={() => void unpair()}
        />
      )}

      <button className="button ghost refresh-device" type="button" onClick={() => void refresh()}>Refresh status</button>
    </div>
  )
}
