import { lstatSync, realpathSync, statSync } from 'node:fs'
import { isAbsolute } from 'node:path'

export type BrowserAcceptancePhase = 'fake-lifecycle' | 'reclaim' | 'tls-smoke' | 'opencode-smoke'

export interface PublicConfiguration {
  baseURL: string
  allowScopedTLSErrors: boolean
}

export interface RuntimeEnvironment extends PublicConfiguration {
  phase: BrowserAcceptancePhase
  username: string
  password: string
  pairingCode: string
  remoteHostname: string
  serverHostname: string
  localHostname: string
  remoteUID: string
  controlDirectory: string
  barrierTimeoutMS: number
}

function required(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`configuration: missing ${name}`)
  return value
}

function checkedBaseURL(): string {
  const raw = required('AISUMMONER_E2E_BASE_URL')
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('configuration: AISUMMONER_E2E_BASE_URL is invalid')
  }
  if (parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash) {
    throw new Error('configuration: AISUMMONER_E2E_BASE_URL must be a credential-free origin')
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('configuration: AISUMMONER_E2E_BASE_URL must use HTTP or HTTPS')
  }
  if (parsed.protocol === 'http:' && !['127.0.0.1', '::1', 'localhost'].includes(parsed.hostname)) {
    throw new Error('configuration: plaintext AISUMMONER_E2E_BASE_URL must be loopback')
  }
  return parsed.origin
}

function checkedPhase(): BrowserAcceptancePhase {
  const phase = required('AISUMMONER_E2E_PHASE')
  if (phase !== 'fake-lifecycle' && phase !== 'reclaim' && phase !== 'tls-smoke' && phase !== 'opencode-smoke') {
    throw new Error('configuration: AISUMMONER_E2E_PHASE is invalid')
  }
  return phase
}

function checkedPairingCode(): string {
  const value = required('AISUMMONER_E2E_PAIRING_CODE')
  if (!/^[A-Za-z0-9]{4}-?[A-Za-z0-9]{4}$/.test(value)) {
    throw new Error('configuration: AISUMMONER_E2E_PAIRING_CODE is malformed')
  }
  return value
}

function checkedUID(): string {
  const value = required('AISUMMONER_E2E_REMOTE_UID')
  if (!/^[0-9]{1,10}$/.test(value)) {
    throw new Error('configuration: AISUMMONER_E2E_REMOTE_UID is invalid')
  }
  return value
}

function checkedControlDirectory(): string {
  const value = required('AISUMMONER_E2E_CONTROL_DIR')
  if (!isAbsolute(value)) {
    throw new Error('configuration: AISUMMONER_E2E_CONTROL_DIR must be absolute')
  }
  try {
    const link = lstatSync(value)
    const status = statSync(value)
    if (link.isSymbolicLink() || !status.isDirectory() || realpathSync(value) !== value) {
      throw new Error('invalid')
    }
    if ((status.mode & 0o077) !== 0 || (typeof process.getuid === 'function' && status.uid !== process.getuid())) {
      throw new Error('invalid')
    }
  } catch {
    throw new Error('configuration: AISUMMONER_E2E_CONTROL_DIR must be an owned private real directory')
  }
  return value
}

function checkedBarrierTimeout(): number {
  const raw = process.env.AISUMMONER_E2E_BARRIER_TIMEOUT_MS ?? '300000'
  if (!/^[0-9]+$/.test(raw)) {
    throw new Error('configuration: AISUMMONER_E2E_BARRIER_TIMEOUT_MS is invalid')
  }
  const timeout = Number(raw)
  if (!Number.isSafeInteger(timeout) || timeout < 30_000 || timeout > 900_000) {
    throw new Error('configuration: AISUMMONER_E2E_BARRIER_TIMEOUT_MS is outside the allowed range')
  }
  return timeout
}

export function loadPublicConfiguration(): PublicConfiguration {
  const baseURL = checkedBaseURL()
  const allowScopedTLSErrors = process.env.AISUMMONER_E2E_ALLOW_SCOPED_TLS_ERRORS === '1'
  if (allowScopedTLSErrors && !baseURL.startsWith('https://')) {
    throw new Error('configuration: scoped TLS-error allowance requires HTTPS')
  }
  return { baseURL, allowScopedTLSErrors }
}

export function loadRuntimeEnvironment(): RuntimeEnvironment {
  const publicConfiguration = loadPublicConfiguration()
  const remoteHostname = required('AISUMMONER_E2E_REMOTE_HOSTNAME')
  const serverHostname = required('AISUMMONER_E2E_SERVER_HOSTNAME')
  const localHostname = required('AISUMMONER_E2E_LOCAL_HOSTNAME')
  if (new Set([remoteHostname, serverHostname, localHostname]).size !== 3) {
    throw new Error('configuration: hostname oracles must be distinct')
  }
  return {
    ...publicConfiguration,
    phase: checkedPhase(),
    username: required('AISUMMONER_E2E_USERNAME'),
    password: required('AISUMMONER_E2E_PASSWORD'),
    pairingCode: checkedPairingCode(),
    remoteHostname,
    serverHostname,
    localHostname,
    remoteUID: checkedUID(),
    controlDirectory: checkedControlDirectory(),
    barrierTimeoutMS: checkedBarrierTimeout(),
  }
}
