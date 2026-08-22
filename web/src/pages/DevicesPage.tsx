import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { APIError, api, normalizePairingCode } from '../api/client'
import type { Device } from '../api/types'
import { InlineError } from '../components/InlineError'
import { StatusBadge } from '../components/StatusBadge'

function formatSeen(value: string | null): string {
  if (!value) return 'Never'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

export function DevicesPage() {
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [pairingCode, setPairingCode] = useState('')
  const [pairing, setPairing] = useState(false)
  const [pairError, setPairError] = useState<string | null>(null)
  const [pairSuccess, setPairSuccess] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setDevices(await api.devices())
      setLoadError(null)
    } catch (error) {
      setLoadError(error instanceof APIError ? error.message : 'Could not load devices.')
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
      setPairError('Enter the complete 8-character pairing code.')
      return
    }
    setPairing(true)
    setPairError(null)
    setPairSuccess(null)
    try {
      const device = await api.claimPairing(normalized)
      setPairingCode('')
      setPairSuccess(`${device.name} is now paired.`)
      await refresh()
    } catch (error) {
      setPairError(error instanceof APIError ? error.message : 'Could not pair this device.')
    } finally {
      setPairing(false)
    }
  }

  return (
    <div className="page-stack">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Execution plane</p>
          <h1>Devices</h1>
          <p className="muted">Pair a Linux host, then open its terminal or summon an Agent.</p>
        </div>
        <button className="button secondary" type="button" onClick={() => void refresh()}>
          Refresh
        </button>
      </div>

      <section className="panel pair-panel" aria-labelledby="pair-title">
        <div>
          <h2 id="pair-title">Pair a device</h2>
          <p className="muted">Enter the one-time code printed by aisummoner-client.</p>
        </div>
        <form className="pair-form" onSubmit={claim}>
          <label className="sr-only" htmlFor="pairing-code">Pairing code</label>
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
            {pairing ? 'Pairing…' : 'Pair device'}
          </button>
        </form>
        <span id="pair-help" className="muted tiny">Codes expire after 10 minutes and work once.</span>
        <InlineError message={pairError} />
        {pairSuccess && <div className="notice success" role="status">{pairSuccess}</div>}
      </section>

      <InlineError message={loadError} />
      {loading ? (
        <div className="centered-state">Loading devices…</div>
      ) : devices.length === 0 ? (
        <section className="empty-state">
          <h2>No paired devices</h2>
          <p>Start the Remote Client on a Linux machine and enter its code above.</p>
        </section>
      ) : (
        <section className="device-grid" aria-label="Paired devices">
          {devices.map((device) => (
            <Link className="device-card" to={`/devices/${encodeURIComponent(device.id)}`} key={device.id}>
              <div className="device-card-title">
                <h2>{device.name}</h2>
                <StatusBadge online={device.online} />
              </div>
              <dl className="metadata-grid">
                <div><dt>System</dt><dd>{device.platform} / {device.arch}</dd></div>
                <div><dt>Client</dt><dd>{device.client_version}</dd></div>
                <div><dt>Last seen</dt><dd>{formatSeen(device.last_seen_at)}</dd></div>
              </dl>
            </Link>
          ))}
        </section>
      )}
    </div>
  )
}
