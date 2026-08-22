---
task_id: task006
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 2
review_required: true
---

# Task 006 Summary

## Files Changed

- `internal/store/agent.go`
  - Added ownership-joined persistence for Agent sessions, messages and tool calls.
  - Added atomic turn start/state transitions, external provider session updates and one-time approval decisions.
- `internal/store/agent_test.go`
  - Covers lifecycle, ownership isolation, external session IDs, explicit decisions and Full Access auto-start semantics.
- `internal/agent/types.go`
  - Defines the Adapter, EventSink, RemoteExecInvoker, RemoteExecutor, online-state and audit boundaries, stable limits/events, typed execution errors and safe provider-facing Adapter errors.
- `internal/agent/hub.go`
  - Adds the bounded, non-blocking live-event hub with cancellation and slow-subscriber cleanup.
- `internal/agent/fake.go`
  - Adds deterministic configurable Fake scripts. The default script requests `hostname` and `uname -a` only through the injected Remote Executor and incorporates output, exit code and truncation evidence in its response.
- `internal/agent/service.go`
  - Implements session/turn orchestration, approval state machine, bounded `remote_exec`, event publication, cancellation and best-effort redacted auditing.
- `internal/agent/service_test.go`
  - Covers approvals, early-decision reconciliation, Full Access, online rechecks, validation/limits, executor timeout, end-to-end truncation, cancellation, persistence failures, provider errors, audit/log redaction and hub cleanup.
- `internal/agentapi/api.go`
  - Adds the standalone authenticated Agent HTTP/SSE handler for all five frozen endpoints.
- `internal/agentapi/responses.go`
  - Adds stable owner-visible session/message/tool-call response DTOs.
- `internal/agentapi/api_test.go`
  - Covers authentication, exact Origin, ownership hiding, JSON/body bounds, stable errors, decisions, canonical live SSE order/frame format and real subscriber removal on client cancellation.

## Behavior Changed

- Agent sessions are created only for an owned, currently online Device. Empty `approval_mode` defaults to `per_command`; accepted values are `per_command` and `full_access`.
- The Service provider is explicit and validated, defaults to `fake`, and is persisted per session. Integration can inject an OpenCode Adapter and select its provider without changing this package.
- A session accepts only one running Turn. User and assistant messages, tool calls, approval decisions, provider session ID and terminal state are persisted. Assistant-message or terminal-state persistence failures are surfaced as a failed Turn rather than leaving a silently running Turn.
- Existing-session messages are accepted with `202` even if the Device has just gone offline; the mandatory pre-execution online recheck then emits `turn.failed` with `DEVICE_OFFLINE` and never invokes the executor. New session creation while offline fails synchronously with `DEVICE_OFFLINE`.
- `remote_exec` accepts only strict JSON arguments: command 1-8192 bytes, optional absolute CWD up to 4096 bytes and integer timeout 1000-60000 ms. Turns allow at most 12 calls, return at most 256 KiB combined output to the Adapter and persist an 8 KiB UTF-8 excerpt with explicit exit/truncation fields.
- `per_command` registers its waiter before publishing `tool_call.pending`, reconciles an already-persisted fast decision, and accepts one of `approve_once`, `approve_session` or `deny`. `approve_session` upgrades only that session. `full_access` starts automatically without falsely persisting a user approval decision.
- `CancelDevice(deviceID)` promptly cancels both running execution and pending approval for later unpair/offline integration. Agent code has no local-shell execution path.
- SSE authenticates and owner-checks before committing headers, flushes one-line JSON frames immediately, sends canonical event names, uses bounded subscriptions and does not replay history. GET session remains the refresh source.
- The optional Auditor is best effort. Audit metadata is restricted to IDs, provider/approval/decision, safe failure category, exit code and truncation; command, CWD, stdout/stderr and provider error details are excluded from audit and application logs.
- Typed Adapter failures preserve only allowlisted safe codes (`rate_limited`, `unauthorized`, `provider_unavailable`, `protocol_error`); unknown errors become generic `provider_error` without exposing details.

## Payload And Integration Assumptions

- JSON message content may be exactly 32 KiB even when every byte uses a six-byte JSON escape; the bounded encoded cap is sized for that worst case, while the decoded Service limit remains authoritative. Unknown fields, trailing JSON and decoded oversized bodies are rejected.
- Session creation returns `201`; accepted messages return `202`; decisions return the refreshed tool-call representation.
- SSE is live-only. Clients first load the persisted snapshot and then subscribe; reconnect replay is intentionally out of scope.
- Production integration must inject the authoritative online registry, strict SSH-backed Remote Executor, Auth Service and optional Store-backed Auditor, mount the standalone handler, and call `CancelDevice` on unpair/disconnect lifecycle events.

## Verification

- Host: `ASD-Host`, isolated copy `/tmp/aisummoner-task006.OtVABZ`; no competing Go process and approximately 5.2 GiB available memory before the final run.
- Command: `GOMAXPROCS=2 go test -count=1 -p 2 ./internal/store ./internal/agent ./internal/agentapi`
  - Result: PASS (`store` 0.023s, `agent` 1.228s, `agentapi` 3.659s).
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  - Result: PASS.
- Command: `GOMAXPROCS=2 go vet ./internal/store ./internal/agent ./internal/agentapi`
  - Result: PASS.
- Command: `gofmt -l internal/store/agent.go internal/store/agent_test.go internal/agent internal/agentapi`
  - Result: PASS (no output). Local owned-source hashes match the formatted isolated copy.
- Additional command: `GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/agent ./internal/agentapi`
  - Result: PASS (`agent` 3.428s, `agentapi` 7.670s).

## Deviations From Plan

- No functional deviation. Go is unavailable on the local host, so all Go formatting, tests, vet and builds ran in the isolated ASD-Host copy with bounded concurrency; source hashes were compared afterward.

## Known Issues / Follow-Up

- Task007 still needs to supply the OpenCode Adapter using the typed Adapter boundary.
- Integration still needs to mount the standalone handler and wire the authoritative online state, SSH executor, auditor and `CancelDevice` lifecycle hook. Those files were intentionally left untouched by this task.
- SSE history replay after reconnect remains out of scope by design.

## Revision 1

### Changes

- Added `FailPendingAgentToolCall`, whose conditional update contends on the same durable `pending AND decision IS NULL` predicate as `DecideAgentToolCall`. Timeout/cancellation can therefore win exactly once; a losing late decision returns `INVALID_STATE` and cannot upgrade the Session. If the decision wins, the waiter reconciles that persisted winner before start/deny handling.
- Made `Service.Close` a joined completion boundary. Lifecycle-gated WaitGroup registration prevents Add/Wait races, all concurrent Close callers wait on the same `closeDone`, the root context cancels mutations and turns, and Close waits until pending/running registrations are gone and graceful durable cleanup is complete. `CreateSession`, `StartTurn` and `Decide` reject mutations after closure.
- Added a configurable SSE write timeout. The initial frame, every event and each keepalive set a `ResponseController` write deadline before write/flush, and subscription cleanup does not rely on the peer closing first.
- Hardened all post-tool-create failure paths. Undecided pending rows can terminalize only through the atomic abort operation; start/deny/execute/success writes use fresh bounded cleanup contexts, retry/reconcile ambiguous failures, compensate failed success/denial finalization to `failed`, and publish terminal events only for a confirmed durable terminal row.
- Raised raw-but-bounded JSON allowances to the six-byte-per-decoded-byte worst case for independent command/CWD and message maxima. Decoded limits remain command 8192 bytes, absolute CWD 4096 bytes, and message 32 KiB; one decoded byte over still fails.
- Reordered request-ID injection outside panic recovery and logs `debug.Stack()` while returning the safe standard `INTERNAL` envelope with a matching non-empty `req_` header/body ID.

### Verification

- Host: `ASD-Host`, isolated copy `/tmp/aisummoner-task006-r1.mLoThM`; approximately 5.2 GiB MemAvailable and 2.0 GiB SwapFree, with no competing Go process at the resource gates.
- Command: `GOMAXPROCS=2 go test -count=1 -p 2 ./internal/store ./internal/agent ./internal/agentapi`
  - Result: PASS (`store` 0.028s, `agent` 1.432s, `agentapi` 4.124s).
- Command: focused revision regressions with `-count=5 -p 1` for the durable approval winner, timeout/cancel barriers, decision reconciliation, all three Close windows, injected persistence failures, encoded boundaries, blocked SSE writer and panic recovery.
  - Result: PASS (`store` 0.028s, `agent` 1.276s, `agentapi` 3.513s).
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/agent ./internal/agentapi`
  - Result: PASS (`agent` 4.506s, `agentapi` 8.565s).
- Commands: `GOMAXPROCS=2 go vet ./internal/store ./internal/agent ./internal/agentapi`; `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`; focused `gofmt -l` assertion.
  - Result: PASS; no formatter output. Local owned-source SHA-256 values match the tested/formatted isolated copy.

### Remaining Issues

- Graceful `Service.Close` is deterministic and joined; general reconciliation after an ungraceful process kill remains post-MVP Alpha hardening. No hard-kill restart recovery is claimed.
- Task007/production integration follow-ups listed above remain unchanged.

## Revision 2

### Changes

- Added one context-aware, capacity-one gate to every Turn's `turnInvoker`. The gate serializes the complete `Invoke` lifecycle, including tool-count admission, approval-mode observation and upgrade, durable tool transitions, remote execution and terminal persistence. A caller waiting for the gate returns promptly when its context is canceled and does not consume the tool budget or create a tool row. Browser `Decide` remains independent of the gate so it can resolve the currently pending command.
- Added deterministic concurrent-Adapter regressions proving that more than 12 simultaneous calls admit exactly 12 tools and return `ErrToolCallLimit` for the rest; a queued call ordered after `approve_session` observes Full Access and creates no pending decision; and a canceled queued call returns without persistence/execution or consuming one of the 12 slots.

### Verification

- Host: `ASD-Host`, isolated copy `/tmp/aisummoner-task006-r2.6ayAmB`; approximately 5.2 GiB MemAvailable and 2.0 GiB SwapFree throughout verification, with no competing Go build/test process at each gate.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 ./internal/agent`
  - Result: PASS (`internal/agent` 1.466s).
- Command: `GOMAXPROCS=2 go test -count=10 -p 1 -run 'TestConcurrentInvoke(SerializesToolLimit|ApproveSessionOrdersQueuedCall|CanceledGateWaitDoesNotConsumeLimit)$' ./internal/agent`
  - Result: PASS (`internal/agent` 0.318s).
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/agent`
  - Result: PASS (`internal/agent` 4.826s).
- Commands: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`; `GOMAXPROCS=2 go vet ./internal/store ./internal/agent ./internal/agentapi`; focused `gofmt -l` assertion.
  - Result: PASS; no formatter output. Local changed-source SHA-256 values matched the tested and formatted isolated copy before cleanup.

### Remaining Issues

- No remaining Task006 revision-2 issue is known. Task007 still owns the OpenCode bridge/Adapter, and production composition remains with Task008.
