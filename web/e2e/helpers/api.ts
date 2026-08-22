import type { APIRequestContext, BrowserContext, Page, Response } from '@playwright/test'

import { requireCondition, stage } from './safe.js'

interface ErrorEnvelope {
  error?: {
    code?: unknown
    message?: unknown
    request_id?: unknown
  }
}

interface DevicePayload {
  device?: {
    id?: unknown
    name?: unknown
    platform?: unknown
    arch?: unknown
    client_version?: unknown
    online?: unknown
  }
}

interface SessionPayload {
  session?: {
    id?: unknown
    device_id?: unknown
    approval_mode?: unknown
  }
}

interface ResponseLike {
  status: () => number
  json: () => Promise<unknown>
}

async function parsedJSON(response: ResponseLike, name: string): Promise<unknown> {
  return stage(name, () => response.json())
}

export async function assertAPIError(
  response: ResponseLike,
  status: number,
  code: string,
  name: string,
): Promise<void> {
  requireCondition(response.status() === status, `${name} status`)
  const value = await parsedJSON(response, `${name} envelope`) as ErrorEnvelope
  requireCondition(value?.error?.code === code, `${name} code`)
  requireCondition(typeof value?.error?.message === 'string' && value.error.message.length > 0, `${name} message`)
  requireCondition(typeof value?.error?.request_id === 'string' && value.error.request_id.length > 0, `${name} request id`)
}

export async function assertUnauthenticatedBoundary(request: APIRequestContext): Promise<void> {
  const response = await stage('unauthenticated devices request', () => request.get('/api/v1/devices'))
  await assertAPIError(response, 401, 'UNAUTHENTICATED', 'unauthenticated devices')
}

export async function assertUnknownOwnedBoundaries(
  context: BrowserContext,
  baseURL: string,
): Promise<void> {
  const unknownDevice = await stage('unknown device request', () => context.request.get('/api/v1/devices/dev_e2e_missing'))
  await assertAPIError(unknownDevice, 404, 'DEVICE_NOT_FOUND', 'unknown device')
  const unknownSession = await stage('unknown Agent session request', () => context.request.get('/api/v1/agent-sessions/ags_e2e_missing'))
  await assertAPIError(unknownSession, 404, 'NOT_FOUND', 'unknown Agent session')
  const unknownTool = await stage('unknown tool request', () => context.request.post('/api/v1/tool-calls/tool_e2e_missing/decision', {
    headers: { Origin: baseURL },
    data: { decision: 'deny' },
  }))
  await assertAPIError(unknownTool, 404, 'NOT_FOUND', 'unknown tool')
}

export async function assertOriginBoundary(context: BrowserContext): Promise<void> {
  const response = await stage('foreign Origin request', () => context.request.post('/api/v1/pairings/claim', {
    headers: { Origin: 'https://origin.invalid' },
    data: { code: 'AAAA-AAAA' },
  }))
  await assertAPIError(response, 403, 'ORIGIN_FORBIDDEN', 'foreign Origin')
}

export async function deviceFromClaim(response: ResponseLike): Promise<{
  id: string
  name: string
  platform: string
  arch: string
  clientVersion: string
}> {
  requireCondition(response.status() === 200, 'pairing claim status')
  const payload = await parsedJSON(response, 'pairing claim payload') as DevicePayload
  const device = payload.device
  requireCondition(typeof device?.id === 'string' && device.id.length > 0, 'pairing device id')
  requireCondition(typeof device.name === 'string' && device.name.length > 0, 'pairing device name')
  requireCondition(typeof device.platform === 'string' && device.platform.length > 0, 'pairing device platform')
  requireCondition(typeof device.arch === 'string' && device.arch.length > 0, 'pairing device architecture')
  requireCondition(typeof device.client_version === 'string' && device.client_version.length > 0, 'pairing client version')
  requireCondition(device.online === true, 'pairing device online')
  return {
    id: device.id,
    name: device.name,
    platform: device.platform,
    arch: device.arch,
    clientVersion: device.client_version,
  }
}

export async function sessionFromCreation(response: ResponseLike, deviceID: string): Promise<string> {
  requireCondition(response.status() === 201, 'Agent session creation status')
  const payload = await parsedJSON(response, 'Agent session creation payload') as SessionPayload
  const session = payload.session
  requireCondition(typeof session?.id === 'string' && session.id.length > 0, 'Agent session id')
  requireCondition(session.device_id === deviceID, 'Agent session device binding')
  requireCondition(session.approval_mode === 'per_command', 'Agent session approval mode')
  return session.id
}

export async function waitForResponse(
  page: Page,
  method: string,
  pathnameSuffix: string,
  operation: () => Promise<void>,
  name: string,
): Promise<Response> {
  const pending = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return response.request().method() === method && url.pathname.endsWith(pathnameSuffix)
  }).then((response) => response, () => null)
  try {
    await operation()
    const response = await pending
    if (!response) throw new Error('response unavailable')
    return response
  } catch {
    throw new Error(`${name}: operation failed`)
  }
}
