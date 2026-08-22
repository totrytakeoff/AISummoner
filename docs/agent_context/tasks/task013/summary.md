---
task_id: task013
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 0
review_required: true
---

# Task 013 Summary: Reliable Turns And Resumable Agent Conversation

## Outcome

Task013 is implemented, verified and deployed to the scoped Task011 Server.

- Returning to a Device Agent route restores its newest owner-visible Session
  and transcript. Approval mode is no longer requested again on every route
  entry; `New conversation` is the explicit reset boundary.
- Provider reasoning is normalized, persisted and rendered in a separate
  collapsed `Think` disclosure. It is never concatenated into final assistant
  output.
- OpenCode idle is no longer treated as success by itself. A Turn succeeds only
  after a non-tool final assistant finish and idle; placeholder/idle/error now
  becomes an explicit safe provider failure instead of a false completion.
- Provider failures use actionable, non-sensitive Browser text. The current
  external free provider is unavailable, so the live deployment honestly
  reports `Agent provider is unavailable. Try again.` rather than the old
  unexplained `agent turn failed` or a false success.
- The interaction responsibilities follow DeepSeek Harness persistent-session,
  reasoning-disclosure and ordered-conversation patterns without embedding its
  Host, local shell, filesystem or plugin runtime.

## Implementation

The OpenCode event tracker now distinguishes assistant placeholders,
intermediate tool finishes and final assistant finishes. It remembers an early
idle and waits for a later final/error, preserves current-turn correlation and
keeps Bridge/parser cleanup joined.

The Agent domain now has `response.reasoning.delta` and
`response.reasoning.done`, separate bounded reasoning/text buffers, distinct
persisted `reasoning` and `assistant` messages, and owner-checked latest Session
snapshot lookup. `GET /api/v1/devices/{device_id}/agent-sessions` returns that
snapshot while `POST` remains the explicit new-Session operation.

The Browser hydrates the ordered timeline from the snapshot, reconnects SSE,
keeps history visible while a Device is offline, separates reasoning from
assistant output and retains Provider/approval scope chips.

## Verification

- Full Web Vitest: 9 files / 32 tests PASS.
- Production Web build: 67 modules PASS; only the pre-existing >500 KiB chunk
  warning remained.
- Focused Go packages `internal/opencode`, `internal/store`, `internal/agent`
  and `internal/agentapi`: PASS after two stale adapter fixtures were given the
  final marker required by the corrected contract.
- New OpenCode/Store/Agent/API regressions at count 10: PASS.
- The same four packages under `-race`: PASS.
- Complete `go test -count=1 -p 2 ./...`: all 25 targets PASS.
- `go vet ./...`: PASS.
- Exact production Web assets were embedded into a statically linked Server;
  SHA-256 is
  `e40afb9eb1c05bca6ae61ec623cc15475d407d47cac7e7dc7eba7204d652c648`.

The live scoped Server was replaced only after preserving the prior binary and
environment. Public health remained 200 with strict TLS verification, Server
remained loopback-only on 8088, Bridge/OpenCode remained loopback-only on
14097/14096, Caddy remained the sole public listener on 10001, nginx was not
touched and all unrelated containers remained running.

One real post-deploy Turn produced `provider_unavailable` in about three
seconds with no text/tool/reasoning and no false completion. Six other available
OpenCode free-model IDs were probed sequentially through the same public
product path; every one failed before assistant/tool output. The original Mimo
model setting was restored and health rechecked.

## Source Hashes

- `internal/agent/types.go`: `367ced6b...a1f9`
- `internal/agent/service.go`: `d6b0ec6b...9245`
- `internal/agent/service_test.go`: `016daec8...939f`
- `internal/opencode/sse.go`: `92d582f1...4efb`
- `internal/opencode/sse_test.go`: `d6ea6233...2e52`
- `internal/opencode/adapter_test.go`: `47261e48...8932`
- `internal/store/agent.go`: `b6a61ac9...f46f`
- `internal/store/agent_test.go`: `cfb9856a...99cc`
- `internal/agentapi/api.go`: `b7de71f8...199b`
- `internal/agentapi/api_test.go`: `6459f348...302f`
- `web/src/api/types.ts`: `aaab2688...27d7`
- `web/src/api/client.ts`: `54d90036...7f39`
- `web/src/agent/events.ts`: `6cbbe0b1...026`
- `web/src/agent/events.test.ts`: `73fab052...e6b`
- `web/src/agent/useAgentEvents.ts`: `8a7c493a...32d4`
- `web/src/agent/useAgentEvents.test.tsx`: `c5c86304...2331`
- `web/src/agent/ReasoningBlock.tsx`: `39d13e85...38a7`
- `web/src/pages/AgentPage.tsx`: `0ea17945...83c`
- `web/src/pages/AgentPage.test.tsx`: `544a3ec2...1a96`
- `web/src/styles.css`: `57280b0b...de32`
- `README.md`: `73cba78a...140d`
- `docs/baseline/02-protocol-data-security.md`: `538c7707...ede2`
- `docs/decisions/ADR-0003-agent-adapter-ui.md`: `3e8cb2ab...99d`

## Retained Evidence And Residuals

- The first focused Go run exposed only two old success fixtures that omitted
  the newly mandatory final marker. Production code was unchanged; the exact
  fixtures were corrected and all later focused/repeated/race/full gates passed.
- A model-loop probe initially ran during the Remote reconnect window and
  reported no online Device. It was not counted as a model failure; the probe
  was bounded on online admission before the six valid results above.
- DSH's own direct provider completed a minimal no-tool sentinel request, while
  local OpenCode 1.18.11 using the same provider path timed out at 150 seconds
  with a generic server error. This motivates the separately planned direct
  Provider Adapter rather than importing the DSH backend.
- During that local diagnostic, a process-list command placed the existing
  DeepSeek credential into the private tool trace. It was never committed or
  copied to ASD, but it must be rotated and must not be reused for deployment.
- Task013 fixes correctness and interaction semantics; it cannot make an
  unavailable external OpenCode free provider answer. Task014 owns the direct
  provider path needed for a usable live Agent.

Task013 is frozen for review. Its deployed binary and rollback remain in the
scoped Task011 state root.
