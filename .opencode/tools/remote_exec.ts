import { tool } from "@opencode-ai/plugin"

const BRIDGE_URL = process.env.AISUMMONER_OPENCODE_BRIDGE_URL
const BRIDGE_SECRET = process.env.AISUMMONER_AGENT_BRIDGE_SECRET
const DOMAIN = "AISummoner.OpenCodeBridge.v1"
const CALLBACK_PATH = "/internal/opencode/remote-exec"

const bridgeURL = (): URL => {
  if (!BRIDGE_URL) throw new Error("remote execution bridge is unavailable")
  const parsed = new URL(BRIDGE_URL)
  const loopback = ["127.0.0.1", "[::1]", "localhost"].includes(parsed.hostname.toLowerCase())
  if (
    !loopback ||
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.pathname !== CALLBACK_PATH ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    throw new Error("remote execution bridge is unavailable")
  }
  return parsed
}

const encodeBase64URL = (bytes: Uint8Array): string =>
  Buffer.from(bytes).toString("base64url")

const proof = async (sessionID: string, timestamp: string): Promise<string> => {
  if (!BRIDGE_SECRET || new TextEncoder().encode(BRIDGE_SECRET).length < 32) {
    throw new Error("remote execution bridge is unavailable")
  }
  const encoder = new TextEncoder()
  const key = await crypto.subtle.importKey(
    "raw",
    encoder.encode(BRIDGE_SECRET),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  )
  const message = encoder.encode(`${DOMAIN}\u0000${sessionID}\u0000${timestamp}`)
  return encodeBase64URL(new Uint8Array(await crypto.subtle.sign("HMAC", key, message)))
}

export default tool({
  description: "Execute one approved command on the Remote device bound to this AISummoner session.",
  args: {
    command: tool.schema.string().min(1).max(8192),
    cwd: tool.schema.string().max(4096).optional(),
    timeout_seconds: tool.schema.number().int().min(1).max(60).optional(),
  },
  async execute(args, context) {
    if (!BRIDGE_SECRET) {
      throw new Error("remote execution bridge is unavailable")
    }
    const timestamp = Math.floor(Date.now() / 1000).toString()
    const authorization = await proof(context.sessionID, timestamp)
    const response = await fetch(bridgeURL(), {
      method: "POST",
      redirect: "error",
      signal: context.abort,
      headers: {
        "Authorization": `AISummoner-HMAC ${authorization}`,
        "Content-Type": "application/json",
        "X-AISummoner-Timestamp": timestamp,
      },
      body: JSON.stringify({
        session_id: context.sessionID,
        command: args.command,
        ...(args.cwd === undefined ? {} : { cwd: args.cwd }),
        ...(args.timeout_seconds === undefined ? {} : { timeout_seconds: args.timeout_seconds }),
      }),
    })
    if (!response.ok) {
      throw new Error("remote execution failed")
    }
    const result = await response.json()
    const failure = result.failure?.code ? `failure=${result.failure.code}` : "failure=none"
    return {
      title: result.denied ? "Remote command denied" : "Remote command result",
      output: [
        `stdout:\n${result.stdout ?? ""}`,
        `stderr:\n${result.stderr ?? ""}`,
        `exit_code=${result.exit_code ?? 0}`,
        `truncated=${Boolean(result.truncated)}`,
        `denied=${Boolean(result.denied)}`,
        failure,
      ].join("\n"),
      metadata: {
        exit_code: result.exit_code ?? 0,
        truncated: Boolean(result.truncated),
        denied: Boolean(result.denied),
        failure_code: result.failure?.code ?? null,
      },
    }
  },
})
