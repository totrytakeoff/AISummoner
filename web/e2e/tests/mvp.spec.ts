import { test } from '@playwright/test'
import type { BrowserContext, Page } from '@playwright/test'

import {
  installAgentProbe,
  waitForDeniedTool,
  waitForDeniedTurnTermination,
  waitForFullAccessSecondTool,
  waitForOfflineToolFailure,
  waitForPendingTool,
  waitForStreamError,
  waitForSuccessfulTool,
  waitForTurnCompleted,
} from '../helpers/agent-probe.js'
import {
  assertAPIError,
  assertOriginBoundary,
  assertUnauthenticatedBoundary,
  assertUnknownOwnedBoundaries,
  waitForResponse,
} from '../helpers/api.js'
import {
  assertFinalAgentEvidence,
  claimDevice,
  decideTool,
  login,
  openDevice,
  sendAgentPrompt,
  startPerCommandAgentSession,
  waitForDeviceState,
} from '../helpers/browser-actions.js'
import type { ClaimedDevice } from '../helpers/browser-actions.js'
import { barrier } from '../helpers/checkpoint.js'
import { loadRuntimeEnvironment } from '../helpers/environment.js'
import type { RuntimeEnvironment } from '../helpers/environment.js'
import { requireCondition, safeClick, safeGoto, stage, waitForCondition, waitHidden, waitVisible } from '../helpers/safe.js'
import { sendTerminalLine, terminalInput, TerminalProbe, verifyTerminalExecution } from '../helpers/terminal-probe.js'

const environment = loadRuntimeEnvironment()

test.describe.configure({ mode: 'serial' })

async function openTerminal(
  context: BrowserContext,
  device: ClaimedDevice,
): Promise<{ page: Page; probe: TerminalProbe }> {
  const page = await context.newPage()
  const probe = new TerminalProbe()
  probe.attach(page)
  await safeGoto(page, `/devices/${encodeURIComponent(device.id)}/terminal`, 'terminal navigation')
  await waitVisible(page.getByRole('heading', { name: 'Terminal' }), 'terminal page')
  await waitVisible(page.getByText('Connected', { exact: true }), 'terminal connected state', 20_000)
  await probe.waitConnected('terminal websocket')
  return { page, probe }
}

async function assertRemoteHostnameOnly(
  page: Page,
  probe: TerminalProbe,
  runtime: RuntimeEnvironment,
  name: string,
): Promise<void> {
  const input = await terminalInput(page)
  await sendTerminalLine(page, input, 'stty -echo', `${name} disable echo`)
  await new Promise((resolve) => setTimeout(resolve, 250))
  probe.resetOutput()
  await sendTerminalLine(page, input, 'hostname', `${name} hostname command`)
  await probe.waitForOutputExcluding(
    runtime.remoteHostname,
    [runtime.serverHostname, runtime.localHostname],
    `${name} remote hostname`,
  )
  await sendTerminalLine(page, input, 'stty echo', `${name} restore echo`)
}

async function openAgentPage(context: BrowserContext): Promise<Page> {
  const page = await context.newPage()
  await installAgentProbe(page)
  return page
}

async function currentOnlineDevice(context: BrowserContext, runtime: RuntimeEnvironment): Promise<ClaimedDevice> {
  const response = await stage('current device request', () => context.request.get('/api/v1/devices'))
  requireCondition(response.status() === 200, 'current device status')
  const payload = await stage('current device payload', () => response.json()) as {
    devices?: Array<{
      id?: unknown
      name?: unknown
      platform?: unknown
      arch?: unknown
      client_version?: unknown
      online?: unknown
    }>
  }
  const matches = (payload.devices ?? []).filter((device) =>
    device.online === true && typeof device.name === 'string' && device.name.includes(runtime.remoteHostname),
  )
  requireCondition(matches.length === 1, 'one current remote device online')
  const device = matches[0]
  requireCondition(typeof device.id === 'string' && device.id.length > 0, 'current device id')
  requireCondition(typeof device.name === 'string' && device.name.length > 0, 'current device name')
  requireCondition(typeof device.platform === 'string' && device.platform.length > 0, 'current device platform')
  requireCondition(typeof device.arch === 'string' && device.arch.length > 0, 'current device architecture')
  requireCondition(typeof device.client_version === 'string' && device.client_version.length > 0, 'current client version')
  return {
    id: device.id,
    name: device.name,
    platform: device.platform,
    arch: device.arch,
    clientVersion: device.client_version,
  }
}

test('fake three-host lifecycle acceptance', async ({ page, context, request }) => {
  test.skip(environment.phase !== 'fake-lifecycle', 'phase not selected')

  await assertUnauthenticatedBoundary(request)
  await login(page, context, environment)
  await assertOriginBoundary(context)
  await assertUnknownOwnedBoundaries(context, environment.baseURL)
  const device = await claimDevice(page, environment)
  requireCondition(device.name.includes(environment.remoteHostname), 'paired device remote identity')
  await openDevice(page, device)

  const terminal = await openTerminal(context, device)
  await verifyTerminalExecution(
    terminal.page,
    terminal.probe,
    environment.remoteHostname,
    environment.serverHostname,
    environment.localHostname,
    environment.remoteUID,
  )

  const perCommandPage = await openAgentPage(context)
  await startPerCommandAgentSession(perCommandPage, device.id)
  await sendAgentPrompt(perCommandPage, 'Report the remote hostname and operating system.', 'per-command Agent')
  const hostnameTool = await waitForPendingTool(perCommandPage, 'hostname', 'hostname tool pending')
  await decideTool(perCommandPage, 'hostname', 'Approve once', 'hostname tool')
  await waitForSuccessfulTool(perCommandPage, hostnameTool, 'hostname tool completed', environment.remoteHostname)
  const unameTool = await waitForPendingTool(perCommandPage, 'uname -a', 'uname tool pending')
  await decideTool(perCommandPage, 'uname -a', 'Approve once', 'uname tool')
  await waitForSuccessfulTool(perCommandPage, unameTool, 'uname tool completed', 'Linux')
  await waitForTurnCompleted(perCommandPage, 'per-command turn completed')
  await assertFinalAgentEvidence(
    perCommandPage,
    environment.remoteHostname,
    environment.serverHostname,
    environment.localHostname,
    'per-command final evidence',
  )

  const sessionApprovalPage = await openAgentPage(context)
  await startPerCommandAgentSession(sessionApprovalPage, device.id)
  await sendAgentPrompt(sessionApprovalPage, 'Inspect the same remote host again.', 'session-approval Agent')
  const approvedSessionTool = await waitForPendingTool(sessionApprovalPage, 'hostname', 'session approval pending')
  await decideTool(sessionApprovalPage, 'hostname', 'Approve session', 'session approval')
  await waitForSuccessfulTool(sessionApprovalPage, approvedSessionTool, 'session first tool completed', environment.remoteHostname)
  await waitForFullAccessSecondTool(
    sessionApprovalPage,
    approvedSessionTool,
    'Linux',
    'session second tool automatic',
  )
  await waitForTurnCompleted(sessionApprovalPage, 'session-approval turn completed')

  const deniedPage = await openAgentPage(context)
  await startPerCommandAgentSession(deniedPage, device.id)
  await sendAgentPrompt(deniedPage, 'Attempt the deterministic inspection.', 'deny Agent')
  const deniedHostname = await waitForPendingTool(deniedPage, 'hostname', 'deny hostname pending')
  await decideTool(deniedPage, 'hostname', 'Deny', 'deny hostname')
  await waitForDeniedTool(deniedPage, deniedHostname, 'deny hostname completed')
  await waitForDeniedTurnTermination(deniedPage, deniedHostname, 'deny turn terminated')

  const offlineAgentPage = await openAgentPage(context)
  await startPerCommandAgentSession(offlineAgentPage, device.id)
  await sendAgentPrompt(offlineAgentPage, 'Inspect availability during the lifecycle check.', 'offline Agent')
  const offlineToolID = await waitForPendingTool(offlineAgentPage, 'hostname', 'offline tool pending')

  await barrier(environment, 'stop-client')
  await waitForDeviceState(page, false, 'device offline within heartbeat bound')
  await terminal.probe.waitClosed('existing terminal closed offline')
  await stage('close offline terminal page before reconnect', () => terminal.page.close())
  const offlineDecision = await stage('approve pending tool while offline', () => context.request.post(
    `/api/v1/tool-calls/${encodeURIComponent(offlineToolID)}/decision`,
    { headers: { Origin: environment.baseURL }, data: { decision: 'approve_once' } },
  ))
  requireCondition(offlineDecision.status() === 200, 'offline tool decision accepted')
  await waitForOfflineToolFailure(offlineAgentPage, offlineToolID, 'offline Agent failure')

  await barrier(environment, 'restart-client')
  await waitForDeviceState(page, true, 'device online after restart')
  const restartedTerminal = await openTerminal(context, device)
  await assertRemoteHostnameOnly(restartedTerminal.page, restartedTerminal.probe, environment, 'restarted client')
  await stage('close restarted terminal page', () => restartedTerminal.page.close())

  await barrier(environment, 'start-replacement-client')
  await waitForDeviceState(page, true, 'device online after newest client')
  const replacementTerminal = await openTerminal(context, device)
  await assertRemoteHostnameOnly(replacementTerminal.page, replacementTerminal.probe, environment, 'replacement client')
  await barrier(environment, 'stop-old-client')
  await waitForDeviceState(page, true, 'replacement stays online')
  await assertRemoteHostnameOnly(replacementTerminal.page, replacementTerminal.probe, environment, 'replacement after old stop')
  await stage('close replacement terminal page', () => replacementTerminal.page.close())

  await barrier(environment, 'restart-server')
  await safeGoto(page, `/devices/${encodeURIComponent(device.id)}`, 'device after Server restart')
  await waitForDeviceState(page, true, 'device persisted after Server restart')
  const persistentTerminal = await openTerminal(context, device)
  await assertRemoteHostnameOnly(persistentTerminal.page, persistentTerminal.probe, environment, 'post-restart terminal')
  await stage('close post-restart terminal page', () => persistentTerminal.page.close())

  const unpairTerminal = await openTerminal(context, device)
  const unpairAgentPage = await openAgentPage(context)
  const unpairSessionID = await startPerCommandAgentSession(unpairAgentPage, device.id)
  await sendAgentPrompt(unpairAgentPage, 'Hold for the unpair lifecycle boundary.', 'unpair Agent')
  const unpairToolID = await waitForPendingTool(unpairAgentPage, 'hostname', 'unpair tool pending')

  const unpairPage = await context.newPage()
  await openDevice(unpairPage, device)
  await safeClick(unpairPage.getByRole('button', { name: 'Unpair' }), 'open unpair dialog')
  await waitVisible(unpairPage.getByRole('alertdialog', { name: new RegExp(`Unpair ${device.name}`) }), 'unpair confirmation')
  const unpairResponse = await waitForResponse(
    unpairPage,
    'DELETE',
    `/api/v1/devices/${encodeURIComponent(device.id)}`,
    () => safeClick(unpairPage.getByRole('button', { name: 'Unpair device' }), 'confirm unpair'),
    'unpair response',
  )
  requireCondition(unpairResponse.status() === 204, 'unpair status')
  await unpairTerminal.probe.waitClosed('unpair terminal closure')
  await waitForStreamError(unpairAgentPage, 'unpair Agent stream closure')
  await waitVisible(unpairAgentPage.getByRole('alert'), 'unpair Agent error state')
  await waitVisible(unpairPage.getByRole('heading', { name: 'Devices' }), 'devices after unpair')
  await waitHidden(unpairPage.getByRole('link', { name: new RegExp(device.name) }), 'removed device absent')

  const oldSession = await stage('revoked Agent session request', () => context.request.get(
    `/api/v1/agent-sessions/${encodeURIComponent(unpairSessionID)}`,
  ))
  await assertAPIError(oldSession, 404, 'NOT_FOUND', 'revoked Agent session')
  const oldTool = await stage('revoked Agent tool request', () => context.request.post(
    `/api/v1/tool-calls/${encodeURIComponent(unpairToolID)}/decision`,
    { headers: { Origin: environment.baseURL }, data: { decision: 'approve_once' } },
  ))
  await assertAPIError(oldTool, 404, 'NOT_FOUND', 'revoked Agent tool')

  await barrier(environment, 'fresh-pairing-code')
})

test('reclaim after unpair with a fresh runtime code', async ({ page, context, request }) => {
  test.skip(environment.phase !== 'reclaim', 'phase not selected')

  await assertUnauthenticatedBoundary(request)
  await login(page, context, environment)
  const device = await claimDevice(page, environment)
  requireCondition(device.name.includes(environment.remoteHostname), 'reclaimed device remote identity')
  await openDevice(page, device)
  const terminal = await openTerminal(context, device)
  await assertRemoteHostnameOnly(terminal.page, terminal.probe, environment, 'reclaimed terminal')
})

test('scoped TLS browser terminal and Agent smoke', async ({ page, context, request }) => {
  test.skip(environment.phase !== 'tls-smoke', 'phase not selected')

  await assertUnauthenticatedBoundary(request)
  await login(page, context, environment)
  await assertOriginBoundary(context)
  const device = await currentOnlineDevice(context, environment)
  await openDevice(page, device)

  const terminal = await openTerminal(context, device)
  await verifyTerminalExecution(
    terminal.page,
    terminal.probe,
    environment.remoteHostname,
    environment.serverHostname,
    environment.localHostname,
    environment.remoteUID,
  )
  await stage('close TLS terminal page', () => terminal.page.close())

  const agentPage = await openAgentPage(context)
  await startPerCommandAgentSession(agentPage, device.id)
  await sendAgentPrompt(agentPage, 'Report the remote hostname and operating system.', 'TLS Agent')
  const hostnameTool = await waitForPendingTool(agentPage, 'hostname', 'TLS hostname tool pending')
  await decideTool(agentPage, 'hostname', 'Approve once', 'TLS hostname tool')
  await waitForSuccessfulTool(agentPage, hostnameTool, 'TLS hostname tool completed', environment.remoteHostname)
  const unameTool = await waitForPendingTool(agentPage, 'uname -a', 'TLS uname tool pending')
  await decideTool(agentPage, 'uname -a', 'Approve once', 'TLS uname tool')
  await waitForSuccessfulTool(agentPage, unameTool, 'TLS uname tool completed', 'Linux')
  await waitForTurnCompleted(agentPage, 'TLS Agent turn completed')
  await assertFinalAgentEvidence(
    agentPage,
    environment.remoteHostname,
    environment.serverHostname,
    environment.localHostname,
    'TLS Agent final evidence',
  )
})

test('real OpenCode remote_exec smoke', async ({ page, context, request }) => {
  test.skip(environment.phase !== 'opencode-smoke', 'phase not selected')

  await assertUnauthenticatedBoundary(request)
  await login(page, context, environment)
  const device = await currentOnlineDevice(context, environment)
  await openDevice(page, device)

  const agentPage = await openAgentPage(context)
  await startPerCommandAgentSession(agentPage, device.id)
  await sendAgentPrompt(
    agentPage,
    'Use remote_exec exactly once with command hostname && uname -s. Then state the exact hostname and operating system from that result.',
    'real OpenCode Agent',
  )
  const tool = await waitForPendingTool(agentPage, 'hostname', 'real OpenCode tool pending', 120_000)
  await decideTool(agentPage, 'hostname', 'Approve once', 'real OpenCode tool')
  await waitForSuccessfulTool(agentPage, tool, 'real OpenCode tool completed', environment.remoteHostname)
  await waitForTurnCompleted(agentPage, 'real OpenCode turn completed')
  await assertFinalAgentEvidence(
    agentPage,
    environment.remoteHostname,
    environment.serverHostname,
    environment.localHostname,
    'real OpenCode final evidence',
  )
})
