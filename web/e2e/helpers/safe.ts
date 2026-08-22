import type { Locator, Page } from '@playwright/test'

export async function stage<T>(name: string, operation: () => Promise<T>): Promise<T> {
  try {
    return await operation()
  } catch {
    throw new Error(`${name}: operation failed`)
  }
}

export function requireCondition(condition: boolean, name: string): asserts condition {
  if (!condition) throw new Error(`${name}: boolean oracle was false`)
}

export async function waitForCondition(
  name: string,
  predicate: () => boolean | Promise<boolean>,
  timeoutMS = 15_000,
  intervalMS = 100,
): Promise<void> {
  const deadline = Date.now() + timeoutMS
  while (Date.now() < deadline) {
    try {
      if (await predicate()) return
    } catch {
      // A transient page/network state is retried until the bounded deadline.
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMS))
  }
  throw new Error(`${name}: boolean oracle timed out`)
}

export async function safeGoto(page: Page, path: string, name: string): Promise<void> {
  await stage(name, async () => {
    const response = await page.goto(path, { waitUntil: 'domcontentloaded' })
    if (!response || response.status() >= 400) throw new Error('navigation failed')
  })
}

export async function safeClick(locator: Locator, name: string): Promise<void> {
  await stage(name, () => locator.click())
}

export async function safeFill(locator: Locator, value: string, name: string): Promise<void> {
  await stage(name, () => locator.fill(value))
}

export async function waitVisible(locator: Locator, name: string, timeoutMS = 15_000): Promise<void> {
  await stage(name, () => locator.waitFor({ state: 'visible', timeout: timeoutMS }))
}

export async function waitHidden(locator: Locator, name: string, timeoutMS = 15_000): Promise<void> {
  await stage(name, () => locator.waitFor({ state: 'hidden', timeout: timeoutMS }))
}

export async function waitText(locator: Locator, text: string, name: string, timeoutMS = 15_000): Promise<void> {
  await waitForCondition(name, async () => (await locator.allTextContents()).some((value) => value.includes(text)), timeoutMS)
}
