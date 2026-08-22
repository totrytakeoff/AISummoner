---
task_id: task007
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 007 Plan: OpenCode Sidecar Adapter And Session-Bound Remote Tool Bridge

## Status

Ready for implementation after task006 approval. The precise OpenCode 1.18.11 HTTP/SSE/tool schema has already been probed and is recorded below so implementation does not guess contracts.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

ADR-0002 replaces a bespoke provider loop with one loopback OpenCode sidecar. Task006 supplies the product Agent state machine and injects a session/device-bound `RemoteExec` invoker into its Adapter request. This task implements the OpenCode Adapter, isolated workspace template, tolerant event mapping and a loopback callback that can invoke only the active request's bound executor.

## Goal

Make OpenCode a production-selectable Agent Adapter without granting the model any Server-local shell/files/web/subagent capability, while preserving AISummoner ownership, approval, timeout and output controls for every `remote_exec` call.

## Relevant Files

- `internal/opencode/`
- `internal/opencodebridge/` if a separate package materially clarifies the loopback handler
- `opencode.json`
- `.opencode/tools/remote_exec.ts`
- runtime workspace templates/assets under the owning Go package as needed
- focused OpenCode/bridge tests
- `.env.example` only for already approved OpenCode configuration variables
- `docs/agent_context/tasks/task007/summary.md`

Do not modify Task006 domain semantics, SSH/Tunnel/Terminal, WebUI, migrations, main wiring, deployment files, baseline or ADR. A genuinely necessary narrow interface mismatch must be reported to the orchestrator instead of silently widening scope.

## OpenCode Contract (Verified Against 1.18.11)

### Sidecar

Start externally as:

```text
OPENCODE_SERVER_USERNAME=opencode
OPENCODE_SERVER_PASSWORD=<random strong password>
opencode serve --hostname 127.0.0.1 --port <port>
```

Every request uses Basic Auth. `GET /global/health` returns JSON with `healthy` and `version`. All project APIs include `?directory=<URL-encoded absolute workspace>`.

### Session Creation

```http
POST /session?directory=...
Content-Type: application/json

{"title":"...","model":{"id":"<model-id>","providerID":"opencode","variant":"default"}}
```

The model field is named `id` here. Store returned `id` (`ses_*`) through Task006's external-session sink.

### Async Prompt

```http
POST /session/{sessionID}/prompt_async?directory=...
```

Model field is named `modelID` here. Send a request-level explicit tool map with every built-in false and only `remote_exec: true`; successful status is exactly 204.

Known built-in IDs to disable are `invalid`, `question`, `bash`, `read`, `glob`, `grep`, `edit`, `write`, `task`, `webfetch`, `todowrite`, `websearch`, `skill`, and `apply_patch`.

Use a prompt DTO distinct from the create-session DTO. Its exact tested shape is:

```json
{
  "messageID":"<unique msg_* for this product Turn>",
  "model":{"providerID":"opencode","modelID":"<normalized-model-id>"},
  "agent":"build",
  "tools":{
    "invalid":false,"question":false,"bash":false,"read":false,
    "glob":false,"grep":false,"edit":false,"write":false,
    "task":false,"webfetch":false,"todowrite":false,
    "websearch":false,"skill":false,"apply_patch":false,
    "remote_exec":true
  },
  "parts":[{"type":"text","text":"<user text>"}]
}
```

`AISUMMONER_OPENCODE_MODEL` may be a bare OpenCode model ID or the exact
`opencode/<id>` spelling shown by the CLI. Normalize that one prefix to the
bare HTTP API ID; reject empty IDs, another provider prefix, additional `/`
segments, query/path metacharacters, and values outside a conservative bound.

### Event SSE

`GET /event?directory=...` is data-only SSE; each data JSON has `{id,type,properties}`. Filter strictly on the current external `properties.sessionID`.

Support both stable/compatibility event families:

- `message.part.delta` (`field`, `delta`)
- `message.part.updated` (complete text/tool part)
- `session.next.text.delta` (`assistantMessageID`, optional `textID`, `delta`)
- `session.status` busy/idle/retry
- `session.idle` terminal success
- `session.error` terminal failure
- tolerate tool-called/success/failed events without duplicating Task006 canonical tool state.

Track message/part identifiers and de-duplicate complete-part versus delta text. Ignore unknown events safely with bounded JSON records.

Do not emit the user's prompt from `message.part.updated`; associate text with
an assistant message/part before forwarding it. Treat `session.status: idle`
only as provider state, never as turn completion. Generate one unique `msg_*`
per prompt and send it as `messageID`. Accept a user `message.updated` only
when its ID equals that value, and accept an assistant message only when its
`parentID` equals that value. Associate part events and compatibility
`assistantMessageID` deltas only with those verified assistant messages, using
a globally bounded temporary buffer when ordering requires it. OpenCode 1.18.11
currently supplies `textID` on `session.next.text.delta`; when present it is the
per-part family key and may be correlated with the stable part ID. Treat it as
optional for compatibility: if it is absent, latch the first family for the
whole assistant message (stable-part first ignores later compatibility deltas;
legacy compatibility first ignores later stable parts) so a missing field can
never duplicate output. Track that chronological first family from the first
projected text event even before fallback is needed. Once an absent `textID`
activates the whole-message fallback, later events with or without `textID`
must obey it; changing field presence alone is not a protocol error. A
matching-session busy/tool/assistant event from an
older Turn must not authorize a later
stale `session.idle` to complete this Turn. Latch the first valid delta family
for a text part instead of globally de-duplicating equal delta strings, because
repeated text is valid.

### Abort

`POST /session/{sessionID}/abort?directory=...`, no body, returns JSON boolean. Task cancellation must call abort and close the SSE request.

## Workspace And Tool Policy

- Create a unique empty workspace for each product Agent Session beneath a configured private root. Derive a stable opaque subdirectory from the product Session ID (for example SHA-256/base32), so restart can reuse only that Session's directory without placing the raw ID in a path.
- Root and workspace directories are mode 0700 and files are mode 0600. Reject symlinks with `Lstat`; on reuse verify the exact allowlist and embedded bytes and reject every extra entry.
- Materialize only the reviewed `opencode.json` and `.opencode/tools/remote_exec.ts`. Do not place application source, Server data, SSH keys or credentials in it.
- Project policy file must contain:

```json
{"$schema":"https://opencode.ai/config.json","permission":{"*":"deny","remote_exec":"allow"}}
```

- Request-level tool map is a second independent deny layer.
- The custom tool schema accepts only `command` string, optional `cwd` string and optional integer `timeout_seconds` 1-60. Task006 still performs authoritative byte/path/timeout/tool-count/output checks.
- Custom tool calls only the configured loopback bridge. It must never spawn a process, read arbitrary paths, make arbitrary network requests, or expose its secret in output/errors.

## Session-Bound Bridge Security

- Bridge listens only on loopback or is mounted on a loopback-only internal listener; reject non-loopback `RemoteAddr` defensively.
- Target product session/device/user is resolved from the Adapter's active map keyed by OpenCode `context.sessionID`; never accept a device/user/product-session selector from tool arguments.
- Each mapping contains a context-aware capacity-one gate and serializes the entire callback invocation. OpenCode does not guarantee serial callbacks. Task006 remains the authoritative call-count/approval boundary; this is an additional external-boundary defense and queued calls must wake on mapping/Turn cancellation.
- A bridge master secret is available only to the reviewed custom tool process and Go bridge. Freeze the proof as `Authorization: AISummoner-HMAC <base64.RawURLEncoding MAC>` plus `X-AISummoner-Timestamp: <decimal Unix seconds>`, where the HMAC-SHA256 input is the exact bytes `AISummoner.OpenCodeBridge.v1\x00<external-session-id>\x00<timestamp>`. Require a decoded 32-byte MAC, inject the clock, accept at most two minutes of skew, and compare with `hmac.Equal`. Thus the bearer proof is short-lived and bound to one external session.
- Register an active mapping before prompt dispatch and remove it on completion/cancel/error. Deactivation first marks/cancels it, wakes queued callbacks, then joins in-flight callbacks before Adapter return. Reject absent, mismatched, completed or still-active duplicate mappings.
- Bound the raw body to 80 KiB, reject trailing/unknown JSON and allow POST only. This covers the Task006-authorized command and CWD maxima under worst-case JSON escaping while Task006 remains the decoded-value authority.
- Use short header/body-read deadlines, but not the old 65-second whole-handler limit: a legal callback may wait 120 seconds for approval and then execute for 60 seconds. The handler inherits the mapping/Turn context and is capped by the smaller of its remaining Turn deadline and an approximately 185-second callback ceiling.
- Forward custom tool cancellation using the HTTP request signal. Return a compact structured result with stdout/stderr/exit code/truncated/denied/error category; never return Go/SQL/SSH internals.
- Bound an encoded callback response to 2 MiB so a worst-case escaped Task006 256 KiB combined tool result fits without becoming unbounded.
- A non-nil `RemoteExecInvoker.Invoke` error is fatal to the Turn, not merely an HTTP error visible to OpenCode. Notify the Adapter through a first-error channel, abort/close its OpenCode event request and return the original error. Denial and remote transport failures already represented as `ToolResult, nil` remain model-visible structured results.
- Never log bridge Authorization, HMAC secret, command, cwd or output.

## Adapter Behavior

- Validate configuration URL is loopback HTTP(S), credentials/model/workspace root are present, and never log credential values.
- Health check distinguishes `available`, `rate_limited`, and `unavailable` for diagnostics; deterministic tests never depend on free inference.
- Subscribe to events before dispatch where needed to avoid losing early deltas. Create or reuse exactly the external Session bound to the product Session/workspace.
- Lifecycle order is workspace prepare -> create/reuse and persist external Session ID -> subscribe SSE -> activate mapping -> dispatch prompt -> wait for one matching terminal event. Every exit deactivates and joins the mapping before closing the stream; cancellation/protocol/fatal callback errors perform a short independent best-effort abort.
- Each product user message is one OpenCode async prompt and one bounded event turn. Map text/status/completion/failure through Task006's sink.
- Use conservative HTTP header/body limits and context timeouts; close every response body/SSE scanner.
- 401/403, 429, 5xx, malformed JSON, oversized event, premature EOF and `session.error` become stable typed Adapter errors.
- On cancellation/deadline, best-effort abort with a short independent timeout, then return the original context error.
- Do not infer success merely from HTTP 204; wait for the matching `session.idle`/terminal event.

## Custom Tool Shape

`.opencode/tools/remote_exec.ts` uses `@opencode-ai/plugin` `tool(...)`. Its context provides `sessionID`, `messageID`, `directory` and `abort`. It sends:

```json
{
  "session_id":"<OpenCode context.sessionID>",
  "command":"...",
  "cwd":"/optional",
  "timeout_seconds":30
}
```

The implementation must pass `context.abort` to `fetch` and return a readable string or `{title,output,metadata}` that contains no credential.

## Required Tests

- Exact create-session versus prompt model field names and directory query encoding.
- Every built-in tool false, only `remote_exec` true, and policy wildcard deny.
- Text delta mapping for both event families, full-part de-duplication, matching-session filter, unknown event tolerance and idle/error termination.
- Reused external Session rejects stale prior-Turn assistant/tool/busy/idle events and completes only after the prompt `messageID` or its `parentID`-linked assistant becomes active. Repeated growing full-part snapshots emit only new suffixes; stale shorter snapshots are ignored and divergent rewrites fail closed. Pending role/part buffers have one total record/byte bound, not a multiplicative per-message limit.
- 204 dispatch alone does not finish a Turn.
- Basic Auth on health/session/event/prompt/abort without logging credentials.
- Malformed/oversized SSE, 401, 429, 5xx, premature EOF, context cancel and best-effort abort.
- Unique 0700 workspace with only allowlisted template files.
- Bridge rejects non-loopback, wrong method, missing/bad/expired/wrong-session HMAC, oversized/unknown body and inactive mapping.
- Valid bridge call reaches exactly the active request's injected `RemoteExec`; concurrent external sessions cannot cross-call.
- Concurrent callbacks for one external Session are cancellably serialized; mapping deactivation cancels and joins the executing callback and every queued callback. A fatal invocation error stops the Adapter Turn instead of being lost in an HTTP-only response.
- Command/output/secret sentinel values do not appear in captured logs or auth/error responses.
- TypeScript tool source/config receives a lightweight static/contract test; do not require a live model.
- Exact maximum escaped bridge request and maximum escaped callback response fit their raw caps; one decoded byte over the Task006 limit remains rejected by Task006.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/opencode ./internal/opencodebridge
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

If the bridge is kept inside `internal/opencode`, adjust the first command and record it. A real free-model smoke is informative only: report exactly `available`, `rate_limited`, or `unavailable`; never mark deterministic verification failed solely because the external free tier is limited.

## Documentation Requirements

- Write `docs/agent_context/tasks/task007/summary.md` with exact event mappings, security behavior and tests.
- Record the actual probed sidecar version/model state without claiming production availability.
- Expose narrow Task008 composition surfaces for Adapter construction/health and Bridge handler/activation/joined close. The bridge must be mountable only on a separate loopback listener. Document that separate Compose containers do not share `127.0.0.1`; Task008 must use a shared network namespace or an equivalent same-host loopback topology rather than publicly exposing either side.

## Out Of Scope

- Installing/copying credentials to ASD-Host, choosing a paid API key or bypassing external rate limits.
- Starting/managing sidecar/systemd/Docker in production.
- Main Agent service wiring, WebUI, SSH implementation, deployment and Playwright.
- Enabling any additional OpenCode tool or local workspace capability.

## Acceptance Criteria

The task is ready for review when:

- A fake OpenCode HTTP/SSE server proves session, prompt, streaming, terminal and abort behavior.
- A valid custom tool callback can invoke only its active product Session's bounded RemoteExec path.
- Two deny layers prevent model access to Server-local built-ins.
- Session-bound bridge authentication and cleanup fail closed in negative/concurrent tests.
- Required deterministic tests/builds pass; real free-tier result is honestly classified.
