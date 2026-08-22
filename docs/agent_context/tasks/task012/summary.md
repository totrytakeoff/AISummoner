---
task_id: task012
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 0
review_required: true
---

# Task 012 Summary: Native Agent Timeline And Real OpenCode Deployment

## Outcome

Task012 is implemented, verified and active in the bounded Task011 test
deployment.

- The Browser Agent surface now uses one ordered conversation timeline for
  user messages, assistant text and tool calls. Tool lifecycle updates stay at
  the tool's original position instead of accumulating in a separate card
  pile.
- Pending command approval replaces the composer with an explicit approval
  panel. Completed tools collapse to compact timeline records.
- Provider and tool presentation adapters provide OpenCode, Fake and unknown
  fallbacks without exposing provider-native wire formats to React.
- Fake is labelled as a deterministic test adapter that does not understand
  natural-language tasks. DSH is an interaction reference only; it is not a
  dependency or provider runtime.
- ADR-0003 freezes the Go invocation adapter plus Web presentation adapter
  boundary for future Agent providers.
- ASD now runs the real OpenCode 1.18.11 adapter with the free
  `opencode/mimo-v2.5-free` model. Fake is no longer the active deployment
  adapter.

The public controller remains:

`https://122.51.70.33:10001`

No private CA or compatibility hostname is required.

## OpenCode Turn-Lifecycle Repair

Live verification initially proved that OpenCode successfully invoked
`remote_exec`, received exit code zero and produced assistant text, but the
Turn still timed out after five minutes. Both `opencode/big-pickle` and
`opencode/mimo-v2.5-free` reproduced the same repeated-assistant loop, so it
was not model-specific.

The root cause was the adapter's caller-generated random `messageID`.
OpenCode 1.18.11 generates monotonic message IDs and compares user and
assistant IDs to decide whether the current loop can exit. An arbitrary
external ID can sort after every provider-generated assistant ID, causing the
loop to continue after a valid final response.

The production prompt now omits `messageID` and lets OpenCode allocate its own
monotonic ID. The already-open live SSE stream adopts the first user message
after the dispatch barrier as the current prompt, then accepts only assistant
messages parented to that ID. Stale/foreign session events remain filtered.
The earlier `session.status=idle` compatibility handling remains as a valid
secondary terminal signal, but the live failure was fixed at its actual ID
ordering boundary.

## Source Scope And Final Hashes

Architecture and project documentation:

- `AGENTS.md`: `bc879bca...a6cab4`
- `README.md`: `b8f3f7a0...ec19be`
- `docs/decisions/ADR-0003-agent-adapter-ui.md`:
  `fe65cf7c...e4954`
- `docs/agent_context/tasks/task012/plan.md`:
  `df933adb...600f1`

Web implementation and tests:

- `web/src/agent/adapters.ts`: `92333a8e...cda6`
- `web/src/agent/adapters.test.ts`: `649cfc6b...54e2`
- `web/src/agent/events.ts`: `af73d463...110c`
- `web/src/agent/events.test.ts`: `7679fe6c...100f`
- `web/src/agent/useAgentEvents.test.tsx`: `cc15afe4...c4f`
- `web/src/agent/ToolApprovalPanel.tsx`: `75d993be...044a`
- `web/src/agent/ToolCallCard.tsx`: `d5949cb4...3044`
- `web/src/agent/ToolCallCard.test.tsx`: `6899500e...a6d6`
- `web/src/pages/AgentPage.tsx`: `62ad548f...c81e`
- `web/src/pages/AgentPage.test.tsx`: `42757266...3acc`
- `web/src/styles.css`: `ac43d3a2...1896`

Go compatibility and regression scope:

- `internal/agentapi/api_test.go`: `9fcc26e7...c91`
- `internal/opencode/adapter.go`: `959a171f...31f4`
- `internal/opencode/adapter_test.go`: `a8c27a6f...cffd`
- `internal/opencode/sse.go`: `73557b1a...2ce`
- `internal/opencode/sse_test.go`: `8055ecf8...696c`

Production Web artifacts:

- `web/dist/index.html`: `3589194e...06ba`
- `web/dist/assets/index-BFF8Gdbk.js`: `5478a2ef...120`
- `web/dist/assets/index-CtLOtflP.css`: `584d239e...196`

The earlier Task011 backend event repair remains intact: a Full Access
`tool_call.started` event includes the validated tool name and arguments, so a
tool card never falls back to `(command unavailable)` merely because no
`tool_call.pending` event preceded it.

## Verification Evidence

Web:

- Full Vitest: 9 files / 28 tests PASS.
- Production TypeScript/Vite build: 66 modules PASS.
- Generated index is non-placeholder; JavaScript is 560,552 bytes and CSS is
  15,976 bytes. The only warning is the existing JavaScript chunk size above
  500 kB.
- Timeline ordering, compact tool records, approval-composer takeover,
  provider fallback and honest Fake labelling all have focused oracles.

Go:

- Before the final OpenCode lifecycle repair, the complete clean tree
  `go test -count=1 -p 2 ./...` passed all 25 targets after a test-clock-only
  repair in `internal/agentapi`.
- Final `internal/opencode` package test PASS: 0.054 s.
- Four ID/turn regressions at count 20 PASS: 0.066 s.
- Final `internal/opencode` race PASS with explicit rc=0: 1.263 s.
- The exact production Web dist was embedded into a final
  `CGO_ENABLED=0 -trimpath` Server build. Final binary SHA-256 is
  `97ff3705cae0497802f45395c33a2b6b29717b40988c2eaaacae13876dd87ae5`;
  `file` reports statically linked and `ldd` reports no dynamic executable.

Live real-provider evidence after the ID repair:

1. First repaired Turn: provider `opencode`, final state `idle`, exactly one
   `remote_exec`, completed with exit code zero; remote output and the single
   final assistant message both contained the non-secret `TASK012_FIXED` and
   `Linux` oracles. Direct OpenCode inspection showed one user message, two
   assistant messages, one tool part and a final `finish=stop` instead of the
   former repeated loop.
2. Final static-binary Turn: final state `idle`, exactly one tool, and both
   tool/final-answer `TASK012_STATIC_OK` oracles passed through the public TLS
   endpoint.
3. Per-command approval Turn: the tool reached `pending`, `approve_once` was
   accepted, exactly one tool completed with exit zero, final state became
   `idle`, and both tool/final-answer `TASK012_APPROVAL_OK` oracles passed.

No prompt, command output, password, cookie, Basic Auth credential, bridge
secret or private provider response was written to this record.

## Active ASD Deployment

- Server unit:
  `aisummoner-task011-server-20260821T090612Z.service`.
- Final snapshot: PID 3337353, uid/user `myself` (1001), active/running,
  restart count zero.
- Final Server binary hash: `97ff3705...7ae5`, statically linked.
- Server listener: only `127.0.0.1:8088`.
- Bridge listener: only `127.0.0.1:14097`.
- OpenCode listener: only `127.0.0.1:14096`.
- Caddy remains the sole public listener on TCP 10001; TCP 10002 is unused.
- OpenCode health is authenticated/healthy and reports version 1.18.11.
- OpenCode container remains uid/gid `1001:1001`, read-only root filesystem,
  `restart=no`, host network and the existing restricted workspace mount.
- nginx is active and its complete pre-ACME SHA-256 manifest still verifies.
- Unrelated containers remain exactly
  `asd-kgrag-qa`, `asd-kgrag-qdrant`, `asd-kgrag-neo4j` and
  `mychat-postgres`.
- Final resource snapshot: MemAvailable about 4.71 GiB, SwapFree about
  2.04 GiB, no Go compiler/test process, and zero recent Server warnings.
- Exact local `/tmp/aisummoner-task012-build.StuDes` (2.7 MiB) and ASD
  `/tmp/aisummoner-task012-build.xD1Okh` (63 MiB) staging trees were deleted
  and confirmed absent. Production binaries, state and every scoped rollback
  listed below were retained.

Rollback copies are retained under the private Task011 state root:

- `rollback/task012-pre-opencode/`: pre-Task012 Fake binary/environment.
- `rollback/task012-pre-idle-fix/`: real OpenCode binary before idle-status
  compatibility.
- `rollback/task012-big-pickle/`: model setting before the Mimo switch.
- `rollback/task012-provider-message-id/`: binary before the provider-owned ID
  repair.
- `rollback/task012-static-rebuild/`: the short-lived dynamic candidate caught
  by the final metadata gate.

## Retained Failure And Retry Evidence

- The first scoped OpenCode container attempted to create `/.local` on a
  read-only root filesystem and exited. Only that failed scoped container was
  recreated with `HOME=/tmp`; authenticated health then passed.
- One staging hash wrapper escaped its field expression incorrectly and
  stopped before Go. No test evidence was claimed from it.
- Running clean-checkout static tests against injected production assets
  correctly failed the placeholder oracle. Clean tests and production build
  assets were subsequently kept as separate stages.
- The first complete Go run exposed an expired fixed clock in an Agent API
  fixture. Only the test clock was aligned with the fixture's auth service;
  the focused and complete suites then passed.
- Real `big-pickle` and Mimo turns both executed their tools successfully but
  timed out while repeating assistant messages. The first idle-status patch
  passed tests but did not fix live behavior because OpenCode never became
  idle. Official 1.18.11 source inspection then identified the caller message
  ID ordering defect fixed above.
- One race invocation's tool output channel ended while the remote Go process
  was still running. It was not accepted; after that exact process joined, a
  single tee/rc replay produced package PASS and rc=0.
- The first post-repair public polling script lost one local TCP connection to
  port 10001 after the Turn started. ASD read-only database and direct provider
  checks proved that same Turn had completed successfully; the final static
  and approval runs used bounded network retries and completed end to end.
- The first UI asset marker probe used strings that were not the actual UI
  labels and returned false. The corrected immutable bundle probe found
  `Test adapter active`, `Approve session`, `Approval required` and `OpenCode`.
- The first final Server candidate inherited `CGO_ENABLED=1`. Metadata review
  caught it after one successful live Turn; it was retained as a rollback only.
  The accepted candidate was rebuilt with `CGO_ENABLED=0`, redeployed, and
  passed both full-access and per-command live Turns.

## Residuals And Handoff

- Free-provider availability and quotas are external and can change. The
  current model is live and passed three real repaired flows; failures must
  remain explicitly classified rather than silently switching to Fake.
- Old failed Agent Sessions remain immutable history. Browser testing should
  hard-refresh the new asset and create a new Agent Session.
- The Task011 Server/OpenCode/Caddy deployment remains a bounded test
  deployment with `restart=no`; the recorded rollback and start instructions
  remain required after a host reboot.
- DSH-inspired interaction is now an adapter-backed baseline, not a promise
  that every future provider has identical native events. A new provider must
  supply a Go invocation adapter, a Web presentation profile and fallback
  tests before it is enabled.

Task012 is frozen for independent review. No implementation file should be
changed until that review or an explicit same-task revision.
