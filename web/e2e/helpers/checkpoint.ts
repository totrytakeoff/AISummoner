import { constants } from 'node:fs'
import { open, readFile, stat } from 'node:fs/promises'
import { join } from 'node:path'

import type { RuntimeEnvironment } from './environment.js'
import { waitForCondition } from './safe.js'

export type BarrierName =
  | 'stop-client'
  | 'restart-client'
  | 'start-replacement-client'
  | 'stop-old-client'
  | 'restart-server'
  | 'fresh-pairing-code'

async function isPrivateRegularMarker(path: string, expected: string): Promise<boolean> {
  try {
    const status = await stat(path)
    if (!status.isFile() || (status.mode & 0o077) !== 0) return false
    if (typeof process.getuid === 'function' && status.uid !== process.getuid()) return false
    return (await readFile(path, 'utf8')) === expected
  } catch {
    return false
  }
}

// barrier never performs a remote action. It writes a fixed non-secret ready
// marker and waits for the main integration agent's fixed continue marker.
export async function barrier(environment: RuntimeEnvironment, name: BarrierName): Promise<void> {
  const readyPath = join(environment.controlDirectory, `ready-${name}`)
  const continuePath = join(environment.controlDirectory, `continue-${name}`)
  let ready
  try {
    ready = await open(readyPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600)
    await ready.writeFile('ready\n')
  } catch {
    throw new Error(`barrier ${name}: ready marker creation failed`)
  } finally {
    await ready?.close()
  }
  await waitForCondition(
    `barrier ${name}`,
    () => isPrivateRegularMarker(continuePath, 'continue\n'),
    environment.barrierTimeoutMS,
    250,
  )
}
