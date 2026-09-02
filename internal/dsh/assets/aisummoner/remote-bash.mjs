import { createHmac } from 'node:crypto'
import { isIP } from 'node:net'

export const name = 'aisummoner-remote-bash'
export const inject = ['tools']

const CALLBACK_PATH = '/internal/dsh/remote-exec'
const PROOF_DOMAIN = 'AISummoner.DSHBridge.v1'
const MAX_RESPONSE_BYTES = 2 * 1024 * 1024

function privateBridge() {
  const raw = process.env.AISUMMONER_DSH_BRIDGE_URL
  const secret = process.env.AISUMMONER_AGENT_BRIDGE_SECRET
  if (typeof raw !== 'string' || typeof secret !== 'string' || Buffer.byteLength(secret) < 32) {
    throw new Error('AISummoner remote capability is unavailable')
  }
  let target
  try {
    target = new URL(raw)
  } catch {
    throw new Error('AISummoner remote capability is unavailable')
  }
  const ipFamily = isIP(target.hostname)
  const loopback = ipFamily === 4
    ? target.hostname.split('.')[0] === '127'
    : ipFamily === 6 && target.hostname === '::1'
  if (target.protocol !== 'http:' || !loopback || target.pathname !== CALLBACK_PATH
    || target.username !== '' || target.password !== '' || target.search !== '' || target.hash !== '') {
    throw new Error('AISummoner remote capability is unavailable')
  }
  return { target, secret }
}

function validateArgs(args) {
  if (typeof args.command !== 'string' || args.command.trim() === '' || args.command.length > 8192) {
    throw new Error('invalid command')
  }
  if (typeof args.description !== 'string' || args.description.trim() === '' || args.description.length > 512) {
    throw new Error('invalid description')
  }
  if (args.workdir !== undefined && (typeof args.workdir !== 'string' || args.workdir.length > 4096)) {
    throw new Error('invalid workdir')
  }
  if (args.timeoutMs !== undefined && (!Number.isInteger(args.timeoutMs) || args.timeoutMs < 1000 || args.timeoutMs > 60000)) {
    throw new Error('invalid timeout')
  }
}

function modelText(result) {
  if (result.denied === true) {
    return `Command denied (${result.failure?.code ?? 'COMMAND_DENIED'}).`
  }
  if (result.failure !== undefined && result.failure !== null) {
    const messages = {
      REMOTE_CWD_INVALID: 'The remote working directory is invalid. Use an absolute path for the selected device.',
      REMOTE_POWERSHELL_FAILURE: 'Windows PowerShell could not start the remote command.',
      REMOTE_EXEC_TIMEOUT: 'The remote command timed out.',
      REMOTE_EXEC_CANCELED: 'The remote command was canceled.',
      REMOTE_EXEC_TRANSPORT: 'The remote command channel failed.',
      DEVICE_OFFLINE: 'The selected remote device is offline.',
    }
    return `${messages[result.failure.code] ?? 'The remote command failed.'} (${result.failure.code})`
  }
  const sections = []
  if (result.stdout !== '') sections.push(result.stdout)
  if (result.stderr !== '') sections.push(result.stderr)
  sections.push(`[exit code: ${String(result.exit_code)}]`)
  if (result.truncated === true) sections.push('[output truncated]')
  return sections.join('\n')
}

export function apply(ctx) {
  ctx.effect(() => ctx.tools.register({
    name: 'bash',
    description: 'Execute a command on the AISummoner-selected remote device. Each call uses a fresh shell. Use workdir instead of relying on a previous cd. Check the exit code and never retry a denied command by another route.',
    parameters: {
      type: 'object',
      additionalProperties: false,
      properties: {
        command: { type: 'string', description: 'The command to execute on the remote device.' },
        description: { type: 'string', description: 'A concise description of the command for the user.' },
        timeoutMs: { type: 'integer', description: 'Execution timeout in milliseconds, from 1000 through 60000.' },
        workdir: { type: 'string', description: 'Optional working directory on the remote device.' },
      },
      required: ['command', 'description'],
    },
    output: {
      schema: {
        type: 'object',
        additionalProperties: false,
        properties: {
          tool_call_id: { type: 'string' },
          stdout: { type: 'string' },
          stderr: { type: 'string' },
          exit_code: { type: 'integer' },
          truncated: { type: 'boolean' },
          denied: { type: 'boolean' },
          failure: {
            type: 'object',
            additionalProperties: false,
            properties: {
              code: { type: 'string' },
              message: { type: 'string' },
            },
            required: ['code', 'message'],
          },
        },
        required: ['tool_call_id', 'stdout', 'stderr', 'exit_code', 'truncated', 'denied'],
      },
      render: (_args, result) => [{ type: 'text', text: modelText(result) }],
    },
    async execute(args, exec) {
      validateArgs(args)
      const sessionId = exec.agent?.session?.id
      if (typeof sessionId !== 'string' || !sessionId.startsWith('ses_')) {
        throw new Error('AISummoner remote capability is unavailable')
      }
      const { target, secret } = privateBridge()
      const timestamp = String(Math.floor(Date.now() / 1000))
      const proof = createHmac('sha256', secret)
        .update(PROOF_DOMAIN)
        .update(Buffer.from([0]))
        .update(sessionId)
        .update(Buffer.from([0]))
        .update(timestamp)
        .digest('base64url')
      const body = JSON.stringify({
        session_id: sessionId,
        command: args.command,
        ...(args.workdir === undefined ? {} : { cwd: args.workdir }),
        timeout_seconds: Math.ceil((args.timeoutMs ?? 30000) / 1000),
      })
      const deadline = AbortSignal.timeout(185000)
      const signal = exec.signal === undefined ? deadline : AbortSignal.any([exec.signal, deadline])
      const response = await fetch(target, {
        method: 'POST',
        headers: {
          'Authorization': `AISummoner-HMAC ${proof}`,
          'Content-Type': 'application/json',
          'X-AISummoner-Timestamp': timestamp,
        },
        body,
        signal,
        redirect: 'error',
      })
      if (!response.ok) throw new Error('AISummoner remote capability request failed')
      const declared = Number(response.headers.get('content-length'))
      if (Number.isFinite(declared) && declared > MAX_RESPONSE_BYTES) {
        throw new Error('AISummoner remote capability response is invalid')
      }
      const encoded = await response.arrayBuffer()
      if (encoded.byteLength === 0 || encoded.byteLength > MAX_RESPONSE_BYTES) {
        throw new Error('AISummoner remote capability response is invalid')
      }
      let result
      try {
        result = JSON.parse(Buffer.from(encoded).toString('utf8'))
      } catch {
        throw new Error('AISummoner remote capability response is invalid')
      }
      if (typeof result !== 'object' || result === null || typeof result.stdout !== 'string'
        || typeof result.stderr !== 'string' || !Number.isInteger(result.exit_code)
        || typeof result.truncated !== 'boolean' || typeof result.denied !== 'boolean') {
        throw new Error('AISummoner remote capability response is invalid')
      }
      return result
    },
  }))
}
