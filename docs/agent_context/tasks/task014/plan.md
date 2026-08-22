---
task_id: task014
type: plan
status: in_progress
from: orchestrator
to: implementation
revision: 2
requires_review: true
---

# Task 014 Plan: Direct DeepSeek Agent Adapter

## Goal

Provide a usable real Agent without depending on OpenCode's failing free-model
service. Add a direct DeepSeek Chat-Completions Adapter inspired by the proven
DSH provider implementation while preserving AISummoner as the only owner of
Device authorization, approval, remote execution, persistence and Web events.

## User Direction

- DSH is the interaction and provider reference because its local direct
  DeepSeek request is currently healthy.
- Do not replace the whole AISummoner Agent surface with DSH and do not lose the
  ability to add OpenCode, Codex, Claude or other adapters later.
- The Agent page should behave like a persistent native conversation: no mode
  prompt on every entry, separate Think/final/tool rows, and explicit provider
  errors.

## Architecture

1. Keep `agent.Adapter` as the provider-neutral invocation boundary.
2. Add `internal/deepseek` as a direct, streaming Adapter. It receives no local
   shell/filesystem capability; its only tool is the existing
   `RemoteExecInvoker` bound by the Agent Service to the selected Device.
3. The Server transcript remains the durable conversation source. Extend the
   Run request with bounded prior user/assistant history derived under the same
   owner predicate; provider-private chain state does not become a second
   authority.
4. Normalize streamed `reasoning_content`, final text, tool calls, finish
   reasons and errors into existing Agent events/errors. Echo reasoning only
   inside an in-flight tool loop where the official DeepSeek protocol requires
   it; do not feed historical reasoning back on later user turns.
5. Configure a third exact adapter mode, `deepseek`, with required secret API
   key, validated HTTPS origin and explicit model. Reject redirects and never
   log or return key/provider response bodies.
6. Keep OpenCode and Fake intact. Add DeepSeek Provider presentation metadata;
   unknown future providers retain the safe fallback.

## Provider And Tool Semantics

- Use `POST /chat/completions`, streaming SSE, one bounded request/idle policy
  and the current official model configured by deployment.
- Advertise exactly one function, `remote_exec`, with strict command/cwd/timeout
  arguments. Validate provider JSON again at the existing invoker boundary.
- Preserve complete assistant tool-call messages (including in-turn reasoning)
  and tool results between provider steps as required by thinking-mode tool
  calls. Execute multiple calls deterministically through the existing bounded
  service invoker.
- Accept only a natural `stop` with non-empty final assistant text as success.
  Classify tool calls, length/content-filter/resource finishes, 401/403, 429,
  5xx, malformed SSE, timeout and cancellation explicitly.

## UI

- Add a `DeepSeek` presentation profile using the existing timeline adapters.
- Automatically create the default per-command conversation when no Session
  exists and the Device is online, so opening Agent lands in a composer rather
  than a setup wizard. Keep Full Access an explicit confirmation and scoped to
  a new/current Session.
- Continue automatic latest-session restore, explicit `New conversation`,
  separated Think/final output and approval composer takeover.

## Security

- Browser never receives DeepSeek credentials or provider-native payloads.
- The new key must be freshly rotated; the credential involved in Task013's
  diagnostic trace is not eligible for deployment.
- Do not copy any DSH credential store or DSH session database to ASD.
- HTTPS is mandatory by default; tests may use injected loopback TLS clients.
- Redirect target must receive zero requests.
- Request/response/SSE records, reasoning, arguments and history have explicit
  byte/item limits. No provider body, prompt, command output or key in logs.
- Remote execution retains Device ownership, per-command/Session approval,
  exact Tunnel/SSH identity, timeout/output/tool-count caps and joined shutdown.

## Verification

- Deterministic fake DeepSeek server tests: text/reasoning separation; multiple
  tool steps; tool denial/failure; history reconstruction; malformed/oversized
  SSE; finish/error matrix; redirect rejection; cancellation/idle timeout; no
  secret logging.
- Agent Service tests proving owner-scoped bounded history and no second state
  source.
- Config/composition tests for Fake/OpenCode/DeepSeek isolation and secret
  handling.
- Web tests for auto default Session, provider label, session resume, explicit
  new conversation, Think/final separation and approval takeover.
- Relevant count/repeat/race, full Go, vet, Web Vitest/build and exact static
  Server build.
- Live deployment only after the user supplies or explicitly authorizes a
  newly rotated DeepSeek key. Prove two turns in one Session, real
  `remote_exec`, separated reasoning/final output, route resume and rollback.

## Deployment Boundaries

- Preserve nginx, Caddy/TCP10001, Remote Client, SQLite and unrelated
  containers.
- Change only the scoped Server binary/environment. DeepSeek mode needs no
  OpenCode/Bridge listener; stop those scoped components only after a successful
  direct-provider deployment and keep exact rollback.
- Until a fresh key is available, leave the current healthy public Server in
  its original OpenCode configuration and report external provider failure
  honestly.

## Human Amendment: Web DeepSeek Key Entry

The user explicitly rejected shell/environment setup as the normal testing
workflow. Add a small authenticated Agent-page entry point that accepts a
DeepSeek API key and model over the existing same-origin HTTPS control plane.

- The Browser holds the key only in the mounted password form and clears it on
  cancel or successful submission. It must not use localStorage,
  sessionStorage, URL state, analytics or error text for the credential.
- The Server validates the key/model, constructs the direct Adapter against the
  fixed official DeepSeek HTTPS origin, and keeps the credential only in process
  memory. It must not write the key to SQLite, environment files, logs, audit
  metadata or responses.
- Configuration is global to this single-admin Server process and is lost on
  restart. The UI must state this explicitly.
- A successful online configuration creates a new default `per_command`
  conversation bound to DeepSeek. Existing sessions retain their recorded
  provider and in-flight Turns are not switched underneath execution.
- Keep environment-based startup configuration for unattended deployments;
  the Web entry is the preferred interactive test path, not a second session or
  execution authority.
- Add exact auth/Origin/body-limit/secret-redaction tests, provider-switch race
  coverage, dispatcher/root wiring coverage, and Web interaction tests before
  changing the live ASD deployment.

## Human Amendment: Remove The Turn-Wide Tool Count Wall

The live direct-DeepSeek chain completed twelve real remote tool calls and then
failed only because the original MVP guard rejected the thirteenth call. The
user confirmed that the functional chain is correct and explicitly rejected a
fixed cumulative tool-call count as a product limit.

- Remove the cumulative `MaxToolCallsPerTurn` admission check from the
  authoritative Agent invoker and remove the DeepSeek provider-step counter.
- A Turn continues until the Provider produces its final answer, the user or
  lifecycle cancels it, or the existing bounded Turn deadline expires.
- Keep all independent safety boundaries: exact owner/Device binding,
  per-command or Session approval, one serialized tool lifecycle per Turn,
  per-command timeout, Turn timeout, command/argument/output/request/SSE byte
  limits, Provider idle timeout and joined cancellation.
- Keep a distinct bounded count for tool calls contained in one untrusted
  Provider response. That is a protocol-frame/input bound, not a cumulative
  Agent-work limit.
- Replace the obsolete limit tests with mutation-sensitive tests proving more
  than twelve sequential and concurrent calls succeed while serialization,
  cancellation and persistence remain correct.
- Deploy only the scoped Server binary after focused, repeat, race and merged
  gates. A restart intentionally clears the memory-only DeepSeek key, so the
  administrator must enter it again through the existing Web form.
