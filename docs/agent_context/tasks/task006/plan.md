---
task_id: task006
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 006 Plan: Agent Domain, Approval State Machine, Fake Adapter And SSE API

## Status

Ready for implementation. It can run in parallel with the narrow task002 revision and task003 because it owns an executor/online-state interface and deterministic fakes; production Tunnel/SSH wiring remains for integration.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

The initial migration already contains `agent_sessions`, `agent_messages` and `tool_calls`. ADR-0002 fixes an adapter boundary with Fake and OpenCode implementations. This task owns the product Agent state machine and HTTP/SSE contract; task007 owns OpenCode HTTP/event parsing and its private tool bridge.

## Goal

Implement the ownership-checked Agent API, persistent sessions/messages/tool calls, bounded `remote_exec` orchestration, per-command and session approval, live canonical SSE events, and a deterministic Fake Adapter that completes a real tool loop through an injected Remote Executor.

## Relevant Files

- `internal/agent/`
- `internal/agentapi/`
- new Agent-specific files under `internal/store/`
- focused tests for those packages
- `docs/agent_context/tasks/task006/summary.md`

Do not modify `cmd/`, `internal/httpapi/`, `internal/tunnel/`, `internal/sshclient/`, WebUI, existing migrations, `go.mod`/`go.sum`, baselines or ADRs. Integration will mount this task's standalone handler later.

## Required Public Boundaries

Keep these concepts explicit even if exact Go names differ:

- `Adapter.Run(ctx, RunRequest, EventSink) error`.
- `RunRequest` includes product session identity, optional external provider session identity, user text, and a narrowly scoped `RemoteExec` invoker; it never exposes arbitrary local tools.
- `EventSink` accepts provider text deltas/state and can persist an external provider session ID for task007.
- `RemoteExecutor.Exec(ctx, deviceID, command, cwd)` returns bounded stdout/stderr, exit code and typed failure; the implementation will later use strict SSH.
- Online state is an injected interface, not read from persisted `last_seen_at` alone.
- Agent API is a standalone `http.Handler` that receives the approved Auth Service, Agent Service, allowed Origin and logger as dependencies.

Avoid importing task003 packages so this task compiles independently.

## Persistent Behavior

- Create Agent Session only for a Device owned by the authenticated user. Validate `approval_mode` as `per_command` or `full_access`; default/empty becomes `per_command` only if the API contract deliberately accepts omission.
- Persist provider (`fake` for this task), optional external session ID, state and UTC timestamps.
- Get Session only for its owner and include enough messages/tool calls for a page refresh.
- Post Message only for its owner, with non-empty UTF-8 content bounded to 32 KiB. Allow one running Turn per Session; conflicts return a stable 409 error.
- Persist user and assistant messages. An interrupted process may leave a Turn failed; replay is not required.
- `approve_session` upgrades only the current Agent Session to Full Access and persists that mode. It grants no other session/device/user permission.
- Tool Call decisions and lookups always join through owning Agent Session; never trust a submitted user/device/session ID.

## Remote Exec State Machine And Limits

- Only the tool name `remote_exec` exists.
- Arguments: command required 1-8192 bytes; cwd optional and absolute; timeout optional 1000-60000 ms, default 30000 ms. Reject unknown/invalid structured arguments.
- Maximum 12 tool calls per user Turn.
- Maximum combined stdout+stderr returned to the Adapter is 256 KiB. Preserve exit code and an explicit `truncated` flag. Persist at most the schema-safe 8 KiB excerpt.
- A Turn has a bounded overall lifetime (maximum five minutes). Pending approval has a bounded lifetime (maximum two minutes) and wakes promptly on context cancellation.
- In `per_command`, emit/persist pending before execution and wait for exactly one decision. `approve_once` executes once; `approve_session` changes this Session then executes; `deny` returns a structured denied result to the Adapter without executing.
- In `full_access`, execute without a pending wait while still emitting started/output/completed events.
- Re-check Device ownership and online state immediately before each execution. Offline fails closed with `DEVICE_OFFLINE` and does not call the executor.
- Executor timeout/cancellation, non-zero remote exit and transport errors remain distinguishable. A non-zero command exit is a completed tool result, not necessarily a Turn transport failure.
- No command text or output is written to application logs or audit metadata. Canonical tool events shown to the owner may contain them.

## SSE Contract

- Endpoint and event names exactly match baseline section 5.4.
- Each event envelope includes unpredictable `event_id`, `session_id`, RFC3339 `created_at`, and a type-specific payload.
- Use actual SSE `event: <type>` plus one single-line JSON `data:` record, `Cache-Control: no-cache`, keepalive comments and immediate flush.
- Owner authentication occurs before headers are committed.
- The in-memory hub is bounded and must not let a slow/disconnected browser block a Turn. No historical replay is required, but current Session state and persisted messages are returned by GET.
- Subscriber removal, client cancellation and service shutdown release channels/goroutines.
- Emit the canonical sequence as applicable: `session.state`, `response.text.delta`, `response.text.done`, `tool_call.pending`, `tool_call.started`, `tool_call.output`, `tool_call.completed`, `turn.completed`, `turn.failed`.

## Fake Adapter

- Deterministic and configurable in tests without network/model credentials.
- The default MVP behavior for a normal system-information request performs at least one structured `remote_exec` (for example `uname -a` plus `hostname`) through the supplied invoker, consumes the result, streams a concise final response, and completes.
- Provide fixtures/scripts for text-only, one/multiple tools, invalid tool arguments, denial, cancellation and Adapter failure.
- Never execute local commands; tests use a fake Remote Executor and assert exact calls.

## HTTP API

Implement the frozen endpoints:

```text
POST /api/v1/devices/{device_id}/agent-sessions
GET  /api/v1/agent-sessions/{session_id}
POST /api/v1/agent-sessions/{session_id}/messages
GET  /api/v1/agent-sessions/{session_id}/events
POST /api/v1/tool-calls/{tool_call_id}/decision
```

- Reuse the Task001 cookie name/token Auth Service and standard JSON error envelope/request ID conventions without duplicating or weakening authentication.
- All state-changing requests require exact configured Origin before parsing bodies.
- Bodies are bounded and reject trailing JSON.
- Stable errors cover unauthenticated, forbidden/not-found ownership hiding, offline, invalid request, invalid state/duplicate decision, conflict, timeout and internal failure.
- Decision accepts exactly `approve_once`, `approve_session`, or `deny`.

## Required Tests

- Store session/message/tool lifecycle, owner joins and external session update.
- HTTP unauthenticated, wrong owner, Origin, malformed/oversized body and standard envelope.
- One-running-turn conflict and owner-only event subscription/decision.
- `per_command`: pending, approve-once, approve-session and deny; duplicate/late decisions fail.
- `full_access` bypasses wait for that Session only.
- Device goes Offline before execution; executor not called.
- timeout/cancel, 12-tool cap, command/cwd/timeout validation and 256 KiB truncation.
- Fake Adapter invokes only injected Remote Executor and reaches final text.
- SSE flush/frame format/order plus slow/disconnected subscriber cleanup.
- Logs do not contain test command/output secrets on failure paths.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/store ./internal/agent ./internal/agentapi
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

If the unchanged commands do not yet mount the standalone handler, both must still compile. Do not run race tests concurrently with other heavy work.

## Documentation Requirements

- Write `docs/agent_context/tasks/task006/summary.md` with exact behavior, payload assumptions and verification.

## Out Of Scope

- OpenCode HTTP API/SSE parser, sidecar lifecycle and custom TypeScript tool.
- Loopback OpenCode bridge/token/workspace implementation.
- Production SSH executor and Server main wiring.
- WebUI, static assets, deployment and event replay after restart.

## Acceptance Criteria

The task is ready for review when:

- The deterministic Fake Adapter completes a full approved remote tool loop and final response.
- Owner/Origin/online/approval constraints fail closed under tests.
- Limits, cancellation and SSE cleanup are exercised.
- No local shell execution path exists in Agent code.
- Required tests/builds pass or exact environmental failure is documented.
