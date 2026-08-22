---
task_id: task014
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 2
review_required: true
---

# Task 014 Summary: Direct DeepSeek Adapter And Native Agent Conversation

> Revision 2 removes the fixed per-Turn cumulative tool-call count following a
> live DeepSeek proof that completed twelve tools and requested a thirteenth.
> The revised Server is deployed and ready for independent review. Revision 1
> implemented the authenticated, Server-memory-only DeepSeek key entry; its
> evidence remains historical below.

## Outcome

Task014 revision 1 is implementation-complete, deployed and ready for
independent review. AISummoner now has a direct DeepSeek streaming Adapter
behind the same provider-neutral `agent.Adapter` used by Fake and OpenCode.
The authenticated Agent page exposes one `Set up DeepSeek` action and one
password field. The default model is selected automatically. DSH was used as
an interaction and wire-behavior reference only; its backend, credential store,
plugin runtime, local shell and session database were not imported.

The Agent page no longer asks the user to choose an approval mode on every
entry. It restores the latest owned conversation or automatically creates a
default `per_command` conversation. Reasoning, tool calls and final Assistant
text are distinct timeline rows. Elevating the current conversation to Full
Access is possible only from a real pending command and now requires a second
explicit confirmation.

The revised Server and WebUI are live at `https://122.51.70.33:10001`. The
existing startup Provider, SQLite, nginx, Caddy, OpenCode sidecar, Remote Client
and unrelated containers were preserved. No credential was deployed or read:
a real DeepSeek Turn remains a user-controlled proof after the administrator
pastes a fresh key into the new password field.

Revision 2 removes the obsolete twelve-call Turn wall. A normal Agent Turn now
continues until the Provider returns a final answer, the lifecycle/user cancels
it, or the existing five-minute Turn deadline expires. Owner binding, approval,
serialized tool lifecycle, per-command timeout, all byte/output/request/SSE
bounds, Provider idle timeout and joined cancellation remain unchanged. A
separate 64-call cap applies only to one untrusted Provider response and cannot
accumulate across Provider steps.

## Files Changed

- `internal/deepseek/adapter.go`, `adapter_test.go` — bounded direct HTTPS/SSE
  client, thinking/final separation, deterministic multi-tool continuation,
  safe error classification, redirect rejection and joined cancellation.
- `internal/agent/types.go`, `service.go`, `service_test.go` — provider-neutral
  bounded Server-owned conversation history, mutex-protected runtime Adapter
  registry and safe provider switching between new Sessions.
- `internal/config/config.go`, `config_test.go` — exact `deepseek` mode with
  provider-isolated HTTPS/key/model configuration and non-reflecting errors.
- `cmd/aisummoner-server/server.go`, `server_test.go` — DeepSeek composition
  without an OpenCode listener, workspace or Bridge, plus the memory-only
  dynamic DeepSeek configurator.
- `internal/agentapi/api.go`, `api_test.go`, `internal/app/dispatcher.go`,
  `dispatcher_test.go` — authenticated exact-Origin bounded Provider
  configuration route and public dispatcher wiring.
- `internal/app/deploy_contract_test.go`, `.env.example`,
  `deploy/compose.yaml`, `deploy/validate-compose.sh` — three-mode deployment
  contract; DeepSeek uses no OpenCode Compose profile.
- `web/src/pages/AgentPage.tsx`, `AgentPage.test.tsx`,
  `web/src/agent/DeepSeekSetupDialog.tsx`, `web/src/api/client.ts` — automatic
  default conversation, latest-session restore, explicit New conversation and
  a single-field password dialog that clears its key.
- `web/src/agent/ToolApprovalPanel.tsx`, `ToolCallCard.test.tsx` — explicit
  confirmation before session-wide command approval.
- `web/src/agent/adapters.ts`, `adapters.test.ts`, `web/src/styles.css` —
  DeepSeek presentation and separated reasoning/tool/final layout.
- `README.md`, `docs/baseline/01-architecture-stack.md`,
  `docs/baseline/02-protocol-data-security.md`,
  `docs/decisions/ADR-0003-agent-adapter-ui.md` — provider and UI contract.
- `docs/agent_context/project_context.md`, `architecture_analysis.md`,
  `roadmap.md`, `todo.md`, `state.json` — durable workflow handoff.

## Behavior And Security

- DeepSeek receives only bounded user/assistant history selected through the
  existing owner predicate. Historical provider reasoning and tool internals do
  not become a second conversation authority.
- The Adapter advertises exactly `remote_exec`. Device selection remains in the
  Agent Service; the provider cannot name a host or obtain Server-local shell or
  filesystem access.
- Thinking and final deltas use separate domain events. Within a tool loop, the
  complete `reasoning_content`, non-null Assistant content, tool calls and tool
  results are replayed as required by the provider protocol.
- DeepSeek Thinking requests intentionally omit `tool_choice`. Current official
  DeepSeek compatibility documentation states that V4 thinking mode rejects
  that field; retaining the earlier draft's `tool_choice: auto` would have
  caused a provider HTTP 400 and an Agent Turn failure.
- API key/model/tool IDs are bounded visible ASCII. URL is an HTTPS origin,
  redirects are rejected before the target receives a request, and the caller's
  HTTP client is cloned rather than mutated.
- Request, history, SSE line/record/count, reasoning, final text, tool arguments,
  tool count, output and time are bounded. Provider bodies, credentials,
  commands and reasoning are not logged or returned in public errors.
- HTTP 401/403, 429, other 4xx, 5xx/408, timeout, cancellation, malformed SSE,
  unsupported finishes and sink rejection retain distinct safe classifications.
- Opening Agent with no Session creates `per_command` automatically. Full Access
  is never the default and remains scoped to the current Session.

## Verification

### Revision 2 unbounded Turn tools

- Live read-only aggregation confirmed the reported failure was local rather
  than a Provider/network limit: one recent DeepSeek Turn persisted exactly
  twelve completed tool calls before the old thirteenth-call rejection.
- Focused `./internal/agent ./internal/deepseek`: PASS (`1.581s / 0.158s`).
- Four new regressions at `-count=20`: PASS (`0.494s / 1.313s`). They prove 20
  sequential Provider steps reach a final answer, 20 concurrent callbacks are
  serialized and completed, a canceled queued callback creates no work before
  sixteen later calls complete, and one malformed 65-call Provider reply is
  still rejected as a bounded protocol input.
- Focused race: PASS (`agent 6.700s`, `deepseek 1.410s`).
- Final `go test -count=1 -p 2 ./...`: PASS, all 26 targets.
- Final `go test -race -count=1 -p 1 ./internal/...`: persisted `rc=0`, all 23
  internal package lines `ok`, with no failure/race/panic/timeout marker.
- Final `go vet ./...` and both trimpath command builds: PASS, no output.
- The unchanged verified Web bundle SHA-256
  `4a51958f...7790b585` was re-embedded. Static Server build: PASS, size
  `18,642,120` bytes, SHA-256
  `480fddfdf77d2072fad4cacc32ddca98e693aeba24ea75d5c39af0e7f2c0c231`.
- ASD atomic deployment: PASS. The prior revision-1 binary
  `0c3077e7...91fc7e4` was backed up under
  `/home/myself/.local/state/aisummoner-task014/20260822T164013Z/`; the scoped
  unit is active as uid 1001/PID `4181458`, public health is 200, the Remote
  Client reconnected, and nginx/Caddy/OpenCode/four unrelated containers and
  all listener boundaries remained unchanged.

### Revision 1 Web-key path

- Focused AgentPage Vitest after the final one-field UI: PASS, 10/10 tests.
- Final full Web test: PASS, 9 files / 35 tests.
- Final TypeScript + Vite build: PASS, 68 modules; the only warning was the
  existing non-blocking chunk-size warning. The production bundle is
  `assets/index-mLUQiaFg.js`.
- Five-package focused Go test: PASS (`agent 1.592s`, `agentapi 4.995s`,
  `deepseek 0.094s`, `app 0.068s`, `cmd/server 3.196s`).
- New regression tests at `-count=10`: PASS (`agent 0.213s`, `agentapi 4.281s`,
  `app 0.009s`, `cmd/server 2.305s`).
- Race tests: PASS (`agent 6.710s`, `agentapi 10.987s`, `deepseek 1.222s`,
  `app 1.169s`, `cmd/server 6.206s`).
- Final `go test ./...`: PASS, all 26 targets. Final
  `go test -race ./internal/...`: persisted `rc=0`, 23 package `ok` lines and
  no race/failure/panic/timeout markers. `go vet ./...` and both command builds
  also passed with no output.
- The final static Linux Server was built from the verified Vite output on ASD:
  size `18,635,377` bytes, SHA-256
  `0c3077e7de5968ae1eac6611f90986f9021f4782aae04bb338d23e44291fc7e4`.

### Live ASD deployment

- Old binary SHA-256 `e40afb9e...d652c648` was backed up before the atomic
  replacement. The rollback copy is under
  `/home/myself/.local/state/aisummoner-task014/20260822T155831Z/`.
- The same scoped Server unit is active as uid 1001 with new PID `4156268` and
  the exact candidate hash. Loopback and strict public health both returned
  200; the public page references `assets/index-mLUQiaFg.js`; the new unauthenticated
  Provider route returns 401.
- Caddy remained container `30fc8ec164a7` and the sole TCP10001 listener.
  nginx remained active with the identical readable-config manifest digest.
  OpenCode and all four unrelated containers remained running; an established
  public Client connection was present after restart.
- Full evidence and human steps are frozen in
  `docs/acceptance/task014-web-key-entry-2026-08-23.md`.

### Latest focused Go tree

- `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 300s ./internal/deepseek ./internal/agent ./internal/config ./internal/app ./cmd/aisummoner-server`
  — PASS: `0.092s / 1.574s / 0.006s / 0.067s / 3.008s`.
- Relevant DeepSeek/history/config/deploy/composition tests with `-count=10`
  — PASS: `0.883s / 0.390s / 0.007s / 0.006s / 1.150s`.
- `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 420s` for the same five
  packages — PASS: `1.226s / 6.607s / 1.023s / 1.168s / 5.638s`.

### Clean merged Go gate

- Fresh isolated staging initially contained 229 source files; required
  `.env.example`, deploy files, canonical OpenCode files, migrations, Web locks
  and static placeholder were present. All Go files were already gofmt-clean.
- `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 600s ./...` — PASS, all 26
  targets, wall 19s.
- `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 900s ./internal/...`
  — PASS with independently persisted `rc=0`, 23 `ok` packages and no
  `FAIL`, race warning, panic or timeout marker.
- `GOMAXPROCS=2 go vet ./...` — PASS, no output.
- `GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  — PASS, no output.
- Production static Server build with the verified Vite assets — PASS; binary
  size `18,686,804` bytes, SHA-256
  `c6afcd6f673db656f8b35038468d2047bcfb39ccfcd66b8ca33d65daaf6f4022`,
  and the hashed JS asset name was present in the executable.

### Web and deployment

- Focused AgentPage/approval/presentation Vitest — PASS, 3 files / 15 tests.
- Local full Vitest — PASS, 9 files / 33 tests.
- Local TypeScript + Vite production build — PASS, 67 modules; only the existing
  non-blocking `>500 kB` chunk warning remained.
- Clean isolated `npm --prefix web ci --no-audit --no-fund` — PASS, 167 packages;
  only the existing `whatwg-encoding` deprecation warning.
- After the deterministic test-oracle correction, clean isolated full Vitest —
  PASS, 9 files / 33 tests; clean TypeScript + Vite build — PASS, 67 modules.
- `deploy/validate-compose.sh` with secret-silent config validation — PASS for
  `fake`/profile-off, `deepseek`/profile-off and `opencode`/profile-on.
- `git diff --check` — PASS. Repository credential-pattern scan excluding
  dependency/build directories — no candidate credential found.

ASD verification gates stayed above roughly 4.0 GiB MemAvailable and 1.98 GiB
SwapFree, with no competing or residual Go/Node build jobs.

## Failed Or Non-Evidence Invocations

All failures were retained rather than reported as passes:

1. The first focused Vitest filter incorrectly included `web/src/...` while
   `npm --prefix web` already changed cwd to `web`; Vitest found no tests. The
   corrected `src/...` invocation ran the intended tests.
2. The next focused UI run had one assertion using `findByText("DeepSeek")`
   although the new UI intentionally contains two DeepSeek labels. It was
   changed to assert both labels; no production change was needed.
3. An early deploy contract test counted both the YAML key and its interpolation
   occurrence. The assertion was narrowed to the exact indented environment key.
4. An early repeat regex matched exact names and left some packages with
   `[no tests to run]`; it was not counted. The corrected anchored wildcard run
   passed ten times.
5. One local SSH command had unmatched quoting and was rejected before any SSH
   connection. No remote command ran.
6. The first final-manifest printer had an `awk` escaping error after asset and
   gofmt gates passed; no test ran. A mechanical `cut`-based recomputation
   matched the local manifest.
7. The first full internal race's client output channel closed after six package
   lines while the remote Go process continued normally. It was not accepted.
   The single mechanical rerun persisted output and exit status on ASD and
   produced `rc=0` with all 23 packages.
8. The first clean `npm ci` full Vitest run passed 32/33 tests but exposed a
   scheduling-dependent test oracle: one test dereferenced the fake EventSource
   before React's effect had created it. Production code was unchanged; the
   test now uses a bounded `waitFor` helper. The original full command stopped
   before build, and the corrected full test/build both passed.
9. The first revision-1 AgentPage run passed 9/10 tests; its only failure used
   an ambiguous generic `status` locator after both the SSE and configuration
   notices correctly existed. The assertion was narrowed to the success text.
10. The next revision-1 Web build caught a nullable Device reference in the
    async setup callback. A runtime guard was added; the final full test/build
    passed after the UI was simplified to its single Key field.
11. One tar-over-SSH attempt and two oversized SCP archive attempts failed or
    truncated before staging. Remote byte/hash gates rejected all three before
    Go ran. The same source was compressed, split into bounded chunks and
    reassembled with matching SHA-256 before verification.
12. The first loopback health probe immediately after the live systemd restart
    reached the normal startup gap and failed once. The bounded readiness loop
    then passed, as did public health, asset, route, PID and executable gates;
    rollback was not needed.
13. During revision-2 static asset transfer, the first two Web chunks reused
    names belonging to the already-extracted source archive. No tested source
    or build input was changed. The Web chunks were uploaded again under
    distinct `web-chunk-*` names and the reconstructed archive was accepted
    only after its SHA-256 matched the local production artifact.
14. The revision-2 full-race client output channel closed after four package
    lines while the single remote process continued. No second race was
    started. Its staging-owned result later recorded `rc=0`, 23/23 `ok` lines
    and no failure marker.

## Deviations From Plan

- The human amendment deliberately replaced shell/environment-only interactive
  setup with the authenticated one-field Web flow. The unattended environment
  path remains supported.
- The DSH backend was deliberately not embedded because
  that would add a second session/execution authority and a Server-local shell.
- No old DSH credential was read, copied, committed or deployed in this task.
- The user explicitly superseded the MVP cumulative tool-count guard after the
  live twelve-call proof. Independent time, approval, input and resource bounds
  remain; only the cumulative work counter was removed.

## Human Acceptance

The user confirmed that Terminal and the direct DeepSeek Agent functionally
completed the real Browser → Server → approval → SSH/Tunnel → Remote chain.
The observed failure was only the former local twelve-call product wall, not a
broken Provider or execution path. Revision 2 removes and deploys that wall;
the user explicitly ended additional MVP testing and accepted the full MVP
vertical slice as complete. Because the deployment restart intentionally
cleared the memory-only key, it must be entered again for later interactive use.

## Known Non-Blocking Follow-Up

- The production JS chunk remains about 565 kB before gzip; code splitting is
  an Alpha optimization, not a Task014 correctness blocker.
- Rich Markdown/code rendering, cancel/regenerate controls and provider/model
  switching can extend the provider-neutral timeline later without replacing
  the AISummoner authority model.
- Agent planning efficiency, context compaction, progress/cancel controls and
  richer DSH-like interaction are post-MVP user-experience work, not blockers
  for the completed execution chain.
