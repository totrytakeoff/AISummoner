import type { Page } from '@playwright/test'

import { waitForCondition } from './safe.js'

const agentEventNames = [
  'session.state',
  'response.text.delta',
  'response.text.done',
  'tool_call.pending',
  'tool_call.started',
  'tool_call.output',
  'tool_call.completed',
  'turn.completed',
  'turn.failed',
]

interface ProbeEvent {
  type: string
  value: Record<string, unknown>
}

interface ProbeState {
  events: ProbeEvent[]
  streamErrors: number
}

async function state(page: Page): Promise<ProbeState> {
  return page.evaluate(() => {
    const probe = (window as unknown as { __AISUMMONER_E2E_AGENT_PROBE__?: ProbeState })
      .__AISUMMONER_E2E_AGENT_PROBE__
    return probe ?? { events: [], streamErrors: 0 }
  })
}

function payload(event: ProbeEvent): Record<string, unknown> {
  const candidate = event.value.payload
  return typeof candidate === 'object' && candidate !== null
    ? candidate as Record<string, unknown>
    : event.value
}

function toolID(event: ProbeEvent): string | undefined {
  const value = payload(event).tool_call_id
  return typeof value === 'string' ? value : undefined
}

function command(event: ProbeEvent): string | undefined {
  const value = payload(event).arguments
  if (typeof value !== 'object' || value === null) return undefined
  const candidate = (value as Record<string, unknown>).command
  return typeof candidate === 'string' ? candidate : undefined
}

function pendingID(events: ProbeEvent[], commandFragment: string): string | undefined {
  const event = events.find((candidate) => candidate.type === 'tool_call.pending' && command(candidate)?.includes(commandFragment))
  return event ? toolID(event) : undefined
}

export async function installAgentProbe(page: Page): Promise<void> {
  await page.addInitScript(({ names }) => {
    const maximumEvents = 512
    const maximumSerializedBytes = 1024 * 1024
    const probe = { events: [] as Array<{ type: string; value: Record<string, unknown>; size: number }>, streamErrors: 0, bytes: 0 }
    Object.defineProperty(window, '__AISUMMONER_E2E_AGENT_PROBE__', {
      configurable: false,
      enumerable: false,
      writable: false,
      value: probe,
    })

    const NativeEventSource = window.EventSource
    function WrappedEventSource(url: string | URL, options?: EventSourceInit): EventSource {
      const source = new NativeEventSource(url, options)
      for (const name of names) {
        source.addEventListener(name, (raw) => {
          const data = (raw as MessageEvent<string>).data
          if (typeof data !== 'string' || data.length > 256 * 1024) return
          try {
            const parsed = JSON.parse(data) as unknown
            if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return
            probe.events.push({ type: name, value: parsed as Record<string, unknown>, size: data.length })
            probe.bytes += data.length
            while (probe.events.length > maximumEvents || probe.bytes > maximumSerializedBytes) {
              const removed = probe.events.shift()
              if (removed) probe.bytes -= removed.size
            }
          } catch {
            // Malformed events are ignored by both the probe and product parser.
          }
        })
      }
      source.addEventListener('error', () => { probe.streamErrors++ })
      return source
    }
    WrappedEventSource.prototype = NativeEventSource.prototype
    Object.defineProperties(WrappedEventSource, {
      CONNECTING: { value: NativeEventSource.CONNECTING },
      OPEN: { value: NativeEventSource.OPEN },
      CLOSED: { value: NativeEventSource.CLOSED },
    })
    Object.defineProperty(window, 'EventSource', {
      configurable: false,
      enumerable: true,
      writable: false,
      value: WrappedEventSource,
    })
  }, { names: agentEventNames })
}

export async function waitForPendingTool(
  page: Page,
  commandFragment: string,
  name: string,
  timeoutMS = 30_000,
): Promise<string> {
  let id: string | undefined
  await waitForCondition(name, async () => {
    id = pendingID((await state(page)).events, commandFragment)
    return id !== undefined
  }, timeoutMS)
  if (!id) throw new Error(`${name}: boolean oracle was false`)
  return id
}

export async function waitForSuccessfulTool(
  page: Page,
  toolCallID: string,
  name: string,
  expectedEvidence?: string,
): Promise<void> {
  await waitForCondition(name, async () => {
    const events = (await state(page)).events
    const output = events.find((event) => event.type === 'tool_call.output' && toolID(event) === toolCallID)
    const completed = events.find((event) => event.type === 'tool_call.completed' && toolID(event) === toolCallID)
    const outputPayload = output ? payload(output) : {}
    const completedPayload = completed ? payload(completed) : {}
    const stdout = typeof outputPayload.stdout === 'string' ? outputPayload.stdout : ''
    const stderr = typeof outputPayload.stderr === 'string' ? outputPayload.stderr : ''
    return outputPayload.exit_code === 0 && outputPayload.truncated === false &&
      completedPayload.exit_code === 0 && completedPayload.status === 'completed' &&
      (!expectedEvidence || stdout.includes(expectedEvidence) || stderr.includes(expectedEvidence))
  }, 45_000)
}

export async function waitForDeniedTool(page: Page, toolCallID: string, name: string): Promise<void> {
  await waitForCondition(name, async () => {
    const completed = (await state(page)).events.find(
      (event) => event.type === 'tool_call.completed' && toolID(event) === toolCallID,
    )
    return completed !== undefined && payload(completed).status === 'denied'
  }, 30_000)
}

export async function waitForDeniedTurnTermination(
  page: Page,
  toolCallID: string,
  name: string,
): Promise<void> {
  await waitForCondition(name, async () => {
    const events = (await state(page)).events
    const failedIndex = events.findIndex((event) => event.type === 'turn.failed')
    if (failedIndex < 0) return false
    const completions = events.filter(
      (event) => event.type === 'tool_call.completed' && toolID(event) === toolCallID,
    )
    const deniedIndex = events.findIndex(
      (event) => event.type === 'tool_call.completed' && toolID(event) === toolCallID &&
        payload(event).status === 'denied',
    )
    const pending = events.filter((event) => event.type === 'tool_call.pending')
    const touchedExecution = events.some(
      (event) => (event.type === 'tool_call.started' || event.type === 'tool_call.output') &&
        toolID(event) === toolCallID,
    )
    return completions.length === 1 && deniedIndex >= 0 && deniedIndex < failedIndex &&
      pending.length === 1 && toolID(pending[0]) === toolCallID &&
      pendingID(events, 'uname -a') === undefined && !touchedExecution
  }, 30_000)
}

export async function waitForTurnCompleted(page: Page, name: string): Promise<void> {
  await waitForCondition(name, async () => (await state(page)).events.some((event) => event.type === 'turn.completed'), 45_000)
}

export async function waitForTurnFailure(page: Page, code: string, name: string): Promise<void> {
  await waitForCondition(name, async () => (await state(page)).events.some((event) => {
    if (event.type !== 'turn.failed') return false
    return payload(event).code === code
  }), 45_000)
}

export async function waitForOfflineToolFailure(
  page: Page,
  toolCallID: string,
  name: string,
): Promise<void> {
  await waitForCondition(name, async () => {
    const events = (await state(page)).events
    const matching = (type: string) => events.filter(
      (event) => event.type === type && toolID(event) === toolCallID,
    )
    const completed = matching('tool_call.completed')
    const pendingIndex = events.findIndex(
      (event) => event.type === 'tool_call.pending' && toolID(event) === toolCallID,
    )
    const startedIndex = events.findIndex(
      (event) => event.type === 'tool_call.started' && toolID(event) === toolCallID,
    )
    const completedIndex = events.findIndex(
      (event) => event.type === 'tool_call.completed' && toolID(event) === toolCallID &&
        payload(event).status === 'failed',
    )
    const failedTurns = events.filter((event) => event.type === 'turn.failed')
    const failedIndex = events.findIndex((event) => event.type === 'turn.failed')
    return matching('tool_call.pending').length === 1 &&
      matching('tool_call.started').length === 1 &&
      matching('tool_call.output').length === 0 &&
      completed.length === 1 && payload(completed[0]).status === 'failed' &&
      events.filter((event) => event.type === 'tool_call.pending').length === 1 &&
      pendingID(events, 'uname -a') === undefined &&
      failedTurns.length === 1 && payload(failedTurns[0]).code === 'DEVICE_OFFLINE' &&
      events.every((event) => event.type !== 'turn.completed') &&
      pendingIndex >= 0 && pendingIndex < startedIndex && startedIndex < completedIndex &&
      completedIndex < failedIndex
  }, 45_000)
}

export async function waitForFullAccessSecondTool(
  page: Page,
  firstToolID: string,
  systemEvidence: string,
  name: string,
): Promise<void> {
  await waitForCondition(name, async () => {
    const events = (await state(page)).events
    const pending = events.filter((event) => event.type === 'tool_call.pending')
    const completed = events.filter((event) => event.type === 'tool_call.completed' && payload(event).status === 'completed')
    const second = completed.find((event) => toolID(event) !== firstToolID)
    if (!second) return false
    const id = toolID(second)
    const output = events.find((event) => event.type === 'tool_call.output' && toolID(event) === id)
    const outputPayload = output ? payload(output) : {}
    const stdout = typeof outputPayload.stdout === 'string' ? outputPayload.stdout : ''
    const stderr = typeof outputPayload.stderr === 'string' ? outputPayload.stderr : ''
    return pending.length === 1 && outputPayload.exit_code === 0 && outputPayload.truncated === false &&
      (stdout.includes(systemEvidence) || stderr.includes(systemEvidence))
  }, 45_000)
}

export async function waitForStreamError(page: Page, name: string): Promise<void> {
  await waitForCondition(name, async () => (await state(page)).streamErrors > 0, 15_000)
}
