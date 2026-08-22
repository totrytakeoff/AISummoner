import type { Locator, Page, WebSocket } from '@playwright/test'

import { stage, waitForCondition } from './safe.js'

interface ResizeFrame {
  sequence: number
  cols: number
  rows: number
}

const maximumOutputBytes = 1024 * 1024

function terminalSocket(socket: WebSocket): boolean {
  try {
    return new URL(socket.url()).pathname.endsWith('/terminal')
  } catch {
    return false
  }
}

export class TerminalProbe {
  private socketCount = 0
  private openCount = 0
  private closedCount = 0
  private resizeSequence = 0
  private latestResize?: ResizeFrame
  private output = Buffer.alloc(0)

  attach(page: Page): void {
    page.on('websocket', (socket) => {
      if (!terminalSocket(socket)) return
      this.socketCount++
      socket.on('framesent', (frame) => {
        if (typeof frame.payload !== 'string') return
        try {
          const value = JSON.parse(frame.payload) as { type?: unknown; cols?: unknown; rows?: unknown }
          if (value.type !== 'terminal.resize' || !Number.isInteger(value.cols) || !Number.isInteger(value.rows)) return
          this.latestResize = {
            sequence: ++this.resizeSequence,
            cols: value.cols as number,
            rows: value.rows as number,
          }
        } catch {
          // Binary terminal input and malformed controls are not captured.
        }
      })
      socket.on('framereceived', (frame) => {
        if (typeof frame.payload === 'string') return
        const next = Buffer.from(frame.payload)
        this.output = Buffer.concat([this.output, next])
        if (this.output.byteLength > maximumOutputBytes) {
          this.output = this.output.subarray(this.output.byteLength - maximumOutputBytes)
        }
      })
      socket.on('close', () => { this.closedCount++ })
      socket.on('socketerror', () => { /* UI state is asserted separately. */ })
      this.openCount++
    })
  }

  resetOutput(): void {
    this.output = Buffer.alloc(0)
  }

  resizeSequenceValue(): number {
    return this.resizeSequence
  }

  private outputText(): string {
    return this.output.toString('utf8')
  }

  async waitConnected(name: string): Promise<void> {
    await waitForCondition(name, () => this.socketCount > 0 && this.openCount > 0, 20_000)
  }

  async waitClosed(name: string): Promise<void> {
    await waitForCondition(name, () => this.closedCount > 0, 15_000)
  }

  async waitForOutput(value: string, name: string, timeoutMS = 20_000): Promise<void> {
    await waitForCondition(name, () => this.outputText().includes(value), timeoutMS)
  }

  async waitForOutputExcluding(required: string, forbidden: string[], name: string): Promise<void> {
    await waitForCondition(name, () => {
      const output = this.outputText()
      return output.includes(required) && forbidden.every((value) => !output.includes(value))
    }, 20_000)
  }

  async waitForResize(afterSequence: number, name: string): Promise<ResizeFrame> {
    await waitForCondition(name, () => Boolean(this.latestResize && this.latestResize.sequence > afterSequence), 15_000)
    if (!this.latestResize) throw new Error(`${name}: boolean oracle was false`)
    return this.latestResize
  }
}

export async function terminalInput(page: Page): Promise<Locator> {
  const region = page.getByRole('region', { name: 'Remote terminal' })
  const input = region.getByRole('textbox', { name: 'Terminal input', includeHidden: true })
  await stage('terminal input attachment', () => input.waitFor({ state: 'attached' }))
  return input
}

export async function sendTerminalLine(page: Page, input: Locator, value: string, name: string): Promise<void> {
  await stage(name, async () => {
    await input.evaluate((element) => (element as HTMLElement).focus())
    await page.keyboard.insertText(value)
    await page.keyboard.press('Enter')
  })
}

export async function verifyTerminalExecution(
  page: Page,
  probe: TerminalProbe,
  remoteHostname: string,
  serverHostname: string,
  localHostname: string,
  remoteUID: string,
): Promise<void> {
  const input = await terminalInput(page)
  await sendTerminalLine(page, input, 'stty -echo', 'terminal disable echo')
  await new Promise((resolve) => setTimeout(resolve, 250))
  probe.resetOutput()

  await sendTerminalLine(page, input, "printf 'E2E_READY\\n'", 'terminal readiness command')
  await probe.waitForOutput('E2E_READY', 'terminal readiness output')
  probe.resetOutput()

  await sendTerminalLine(page, input, 'hostname', 'terminal hostname command')
  await probe.waitForOutputExcluding(remoteHostname, [serverHostname, localHostname], 'terminal remote hostname')
  probe.resetOutput()

  await sendTerminalLine(page, input, 'uname -a', 'terminal uname command')
  await probe.waitForOutput('Linux', 'terminal uname output')
  probe.resetOutput()

  await sendTerminalLine(page, input, "printf 'UID='; id -u", 'terminal uid command')
  await probe.waitForOutput(`UID=${remoteUID}`, 'terminal uid output')
  probe.resetOutput()

  await sendTerminalLine(
    page,
    input,
    `sh -c 'printf "E2E_STDOUT\\n"; printf "E2E_STDERR\\n" >&2; exit 7'; printf 'E2E_EXIT=%s\\n' "$?"`,
    'terminal stdout stderr exit command',
  )
  await probe.waitForOutput('E2E_STDOUT', 'terminal stdout output')
  await probe.waitForOutput('E2E_STDERR', 'terminal stderr output')
  await probe.waitForOutput('E2E_EXIT=7', 'terminal exit output')
  probe.resetOutput()

  await sendTerminalLine(
    page,
    input,
    `printf 'E2E_INTERACTIVE_READY\\n'; IFS= read -r answer; printf 'E2E_INTERACTIVE=%s\\n' "$answer"`,
    'terminal interactive read',
  )
  await probe.waitForOutput('E2E_INTERACTIVE_READY', 'terminal interactive readiness')
  await sendTerminalLine(page, input, 'browser-interactive-value', 'terminal interactive answer')
  await probe.waitForOutput('E2E_INTERACTIVE=browser-interactive-value', 'terminal interactive output')
  probe.resetOutput()

  const previousResize = probe.resizeSequenceValue()
  await stage('terminal viewport resize', () => page.setViewportSize({ width: 1137, height: 731 }))
  const resize = await probe.waitForResize(previousResize, 'terminal resize frame')
  await sendTerminalLine(page, input, 'stty size', 'terminal stty size command')
  await probe.waitForOutput(`${resize.rows} ${resize.cols}`, 'terminal remote resize')
  await sendTerminalLine(page, input, 'stty echo', 'terminal restore echo')
}
