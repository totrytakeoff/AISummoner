import type { BrowserContext, Page } from '@playwright/test'

import type { RuntimeEnvironment } from './environment.js'
import { assertAPIError, deviceFromClaim, sessionFromCreation, waitForResponse } from './api.js'
import { requireCondition, safeClick, safeFill, safeGoto, stage, waitForCondition, waitText, waitVisible } from './safe.js'

export interface ClaimedDevice {
  id: string
  name: string
  platform: string
  arch: string
  clientVersion: string
}

export async function login(
  page: Page,
  context: BrowserContext,
  environment: RuntimeEnvironment,
): Promise<void> {
  await safeGoto(page, '/login', 'login navigation')
  await waitVisible(page.getByRole('heading', { name: 'Welcome back' }), 'login page')
  await safeFill(page.getByLabel('Username'), environment.username, 'login username')
  await safeFill(page.getByLabel('Password'), environment.password, 'login password')
  const response = await waitForResponse(
    page,
    'POST',
    '/api/v1/auth/login',
    () => safeClick(page.getByRole('button', { name: 'Sign in' }), 'login submit'),
    'login response',
  )
  requireCondition(response.status() === 200, 'login status')
  await waitVisible(page.getByRole('heading', { name: 'Devices' }), 'devices after login')

  const cookies = await stage('session cookie inspection', () => context.cookies(environment.baseURL))
  const cookie = cookies.find((candidate) => candidate.name === 'aisummoner_session')
  requireCondition(cookie !== undefined, 'session cookie present')
  requireCondition(cookie.httpOnly, 'session cookie HttpOnly')
  requireCondition(cookie.sameSite === 'Strict', 'session cookie SameSite')
  requireCondition(cookie.secure === environment.baseURL.startsWith('https://'), 'session cookie Secure')
}

export async function claimDevice(page: Page, environment: RuntimeEnvironment): Promise<ClaimedDevice> {
  const input = page.getByLabel('Pairing code')
  await safeFill(input, environment.pairingCode, 'pairing code entry')
  const response = await waitForResponse(
    page,
    'POST',
    '/api/v1/pairings/claim',
    () => safeClick(page.getByRole('button', { name: 'Pair device' }), 'pair device submit'),
    'pairing claim response',
  )
  const device = await deviceFromClaim(response)
  await waitVisible(page.getByRole('status'), 'pairing success status')
  await waitVisible(page.getByRole('link', { name: new RegExp(device.name) }), 'paired device link')

  await safeFill(input, environment.pairingCode, 'reused pairing code entry')
  const reused = await waitForResponse(
    page,
    'POST',
    '/api/v1/pairings/claim',
    () => safeClick(page.getByRole('button', { name: 'Pair device' }), 'reused pairing submit'),
    'reused pairing response',
  )
  await assertAPIError(reused, 400, 'PAIRING_CODE_INVALID', 'reused pairing code')
  await waitVisible(page.getByRole('alert'), 'reused pairing error')
  return device
}

export async function openDevice(page: Page, device: ClaimedDevice): Promise<void> {
  await safeGoto(page, `/devices/${encodeURIComponent(device.id)}`, 'device navigation')
  await waitVisible(page.getByRole('heading', { name: device.name }), 'device heading')
  await waitVisible(page.getByText('Online', { exact: true }), 'device online status')
  await waitText(page.locator('main'), device.platform, 'device platform metadata')
  await waitText(page.locator('main'), device.arch, 'device architecture metadata')
  await waitText(page.locator('main'), device.clientVersion, 'device client metadata')
}

export async function waitForDeviceState(page: Page, online: boolean, name: string): Promise<void> {
  const expected = online ? 'Online' : 'Offline'
  await waitForCondition(name, async () => {
    const matches = await page.getByText(expected, { exact: true }).count()
    return matches > 0
  }, online ? 30_000 : 15_000, 250)
}

export async function startPerCommandAgentSession(page: Page, deviceID: string): Promise<string> {
  await safeGoto(page, `/devices/${encodeURIComponent(deviceID)}/agent`, 'Agent navigation')
  await waitVisible(page.getByRole('heading', { name: 'Agent', exact: true }), 'Agent page')
  await waitVisible(page.getByRole('heading', { name: 'Start a remote Agent session' }), 'Agent setup')
  const response = await waitForResponse(
    page,
    'POST',
    `/api/v1/devices/${encodeURIComponent(deviceID)}/agent-sessions`,
    () => safeClick(page.getByRole('button', { name: 'Start Agent session' }), 'Agent session submit'),
    'Agent session response',
  )
  const sessionID = await sessionFromCreation(response, deviceID)
  await waitForCondition('Agent stream open', async () => page.getByLabel('Message the Agent').isEnabled(), 20_000)
  return sessionID
}

export async function sendAgentPrompt(page: Page, text: string, name: string): Promise<void> {
  await safeFill(page.getByLabel('Message the Agent'), text, `${name} prompt`)
  await safeClick(page.getByRole('button', { name: 'Send' }), `${name} submit`)
}

export function toolCard(page: Page, commandFragment: string) {
  return page.getByRole('article', { name: 'remote_exec tool call' }).filter({ hasText: commandFragment }).last()
}

export async function decideTool(
  page: Page,
  commandFragment: string,
  decision: 'Approve once' | 'Approve session' | 'Deny',
  name: string,
): Promise<void> {
  const card = toolCard(page, commandFragment)
  await waitVisible(card, `${name} card`, 30_000)
  await safeClick(card.getByRole('button', { name: decision }), `${name} decision`)
}

export async function assertFinalAgentEvidence(
  page: Page,
  remoteHostname: string,
  serverHostname: string,
  localHostname: string,
  name: string,
): Promise<void> {
  const conversation = page.locator('div.conversation[aria-label="Agent conversation"]')
  const assistantMessages = conversation.locator('article.chat-message.assistant')
  await waitForCondition(name, async () => {
    const messages = await assistantMessages.allTextContents()
    return messages.some((text) => text.includes(remoteHostname) && text.includes('Linux') &&
      !text.includes(serverHostname) && !text.includes(localHostname))
  }, 45_000)
}
