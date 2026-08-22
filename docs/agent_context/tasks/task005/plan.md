---
task_id: task005
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 005 Plan: Browser Terminal WebSocket Gateway

## Status

Ready for implementation after task003 approval. Task004 already owns the xterm browser component; this task owns only the Server-side browser-to-SSH bridge.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

Task003 exposes a strict SSH PTY API over one Server-opened Tunnel stream. The frozen browser contract uses binary WebSocket frames for terminal data and text JSON only for resize.

## Goal

Implement an ownership-checked, same-origin Terminal WebSocket handler that opens a strict Remote SSH PTY, bridges terminal bytes bidirectionally, applies bounded resize events, enforces per-user concurrency and reliably releases every WebSocket/SSH/Tunnel resource.

## Relevant Files

- `internal/terminal/`
- focused tests for that package
- `docs/agent_context/tasks/task005/summary.md`

Do not modify `cmd/`, `internal/httpapi/`, WebUI, task003 SSH implementation, `go.mod`/`go.sum`, baselines or ADRs. Integration mounts the standalone handler later.

## Required Boundaries

- Standalone `http.Handler` receives Task001 Auth Service, an ownership lookup, online state, a narrow task003 PTY opener, exact allowed Origin and logger.
- It handles only `GET /api/v1/devices/{device_id}/terminal`; unrelated paths return the standard not-found envelope.
- PTY abstraction exposes input/output, resize, wait/exit and idempotent close without exposing SSH implementation details.
- Keep the opener as a closure/function boundary because Go return values are not covariant. Task008 adapts `sshclient.Dialer.OpenPTY` to the task-owned PTY interface and translates `(cols, rows)` to `sshclient.PTYOptions`; Task005 must not edit Task003 to force interface satisfaction.
- The minimum injected shapes are an authenticator returning `store.User`, `DeviceByOwner`, `IsOnline`, and `OpenPTY(context, deviceID, cols, rows)`. Browser Terminal uses empty CWD.
- The standalone handler also exposes joined, idempotent lifecycle methods `CancelDevice(deviceID)` and `Close()`. Task008 uses them after unpair and during Server shutdown because `http.Server.Shutdown` does not own already hijacked WebSockets.

## Required Behavior

- Authenticate the Task001 HttpOnly session cookie before WebSocket upgrade.
- Hide cross-owner Device existence as not found/forbidden according to the existing API convention, and reject Offline before opening PTY.
- Require exact configured `Origin` for the WebSocket handshake even though HTTP method is GET. Reject missing or mismatched browser Origin.
- Apply pre-upgrade gates in the fixed fail-closed order: exact route/method, exact Origin, session authentication, owner-scoped Device lookup, authoritative online state, then atomic per-user slot. The PTY opener is called only after all gates and WebSocket upgrade succeed.
- Enforce at most four concurrent terminals per authenticated user, atomically, with reliable release on every failure/close path.
- Track active/pre-open sessions by Device as well as user. Snapshot a per-Device invalidation generation before the owner lookup, then atomically compare that generation while registering the pre-upgrade session/slot. `CancelDevice` increments the generation before canceling and joining all sessions for that Device. Thus a request that observed ownership before a concurrent unpair cannot register after invalidation, while a future legitimately re-paired request can use the new generation.
- Return a standard pre-upgrade `429 TERMINAL_LIMIT` for the fifth terminal; different users have independent counters. A once-only release removes zero-valued map entries.
- Upgrade with binary support, set 64 KiB read limit, then open one Remote PTY with a safe initial size (bounded cols 1-500, rows 1-300).
- Browser binary frames are copied verbatim to PTY stdin. Terminal stdout/PTY master bytes are sent as binary frames.
- Browser text frames accept only one strict JSON object: `{"type":"terminal.resize","cols":N,"rows":N}`. Reject unknown fields/types, trailing JSON and out-of-range dimensions.
- Close from browser, Remote EOF, Tunnel loss, handler context cancellation or write error cancels the opposite direction and closes WebSocket, SSH session and yamux stream exactly once.
- After upgrade, one central coordinator owns cancellation and cleanup. Workers only report their first terminal result. To preserve a deterministic browser close code, the coordinator first starts exactly one bounded static WebSocket close handshake; after a short grace it cancels worker/session contexts, closes the PTY, uses an immediate socket close if the handshake is still blocked, joins the reader/writer/PTY-wait/close workers, then releases capacity. Never write an HTTP JSON envelope after status 101.
- Do not reconnect or retain shell state.
- Use bounded buffers/backpressure. PTY output uses one fixed buffer and the sole WebSocket data writer with a bounded write context; a slow browser must not create an output queue. PTY input must loop over short writes and treat `0, nil` as `io.ErrShortWrite`.
- Do not log terminal input/output or command content. Logs may include request/session/device IDs and public close category.
- Map pre-upgrade failures to the standard JSON error envelope and post-upgrade failures to suitable non-secret WebSocket close reasons.
- Keep post-upgrade close codes/reasons static and non-secret: normal Remote EOF/browser close, policy/protocol/oversize input, going-away cancellation, and internal/opener failures are distinct categories; raw SSH/Tunnel/browser reasons never enter logs or close text.
- `coder/websocket` accepts a missing Origin by default, so the exact manual check is mandatory. Disable compression, set the 64 KiB read limit, keep exactly one reader and one writer, and preserve direct `http.Hijacker` capability through any full-stack wrapper.

## Required Tests

- Unauthenticated, wrong owner, missing/mismatched Origin and Offline fail before upgrade; PTY opener is not called.
- Valid binary input reaches fake PTY byte-for-byte and fake PTY output reaches browser as binary.
- Valid resize reaches fake PTY; malformed, unknown, oversized and out-of-bounds controls close/fail safely.
- Binary input exercises partial writes and a `0, nil` writer; strict resize rejects fractions, missing/unknown fields, trailing JSON, invalid type and values outside cols 1-500 / rows 1-300 before uint16 conversion.
- Five simultaneous terminal attempts for one user reject the fifth; capacity returns after close. Different users are isolated.
- Browser close, Remote EOF, opener error and context cancellation each close the fake PTY and release the limiter.
- `CancelDevice` cancels and joins both already-open and pre-open sessions; a deterministic owner-lookup/admission barrier proves a request that saw the old owner cannot cross a concurrent invalidation. `Close` rejects new sessions, cancels and joins all existing sessions, and repeated calls are safe.
- No goroutine remains blocked in the tested close paths.
- A deterministic blocked WebSocket writer test proves context cancellation unblocks and joins the slow-browser path without relying on peer close; output remains direct/backpressured with no queue.
- Captured logs do not contain sentinel terminal input/output.
- Full handler stack preserves WebSocket upgrade capability.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/terminal ./internal/sshclient
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

Do not run Playwright in this task.

## Documentation Requirements

- Write `docs/agent_context/tasks/task005/summary.md` with exact test/build evidence and close semantics.

## Out Of Scope

- xterm/React code.
- SSHD/PTTY implementation changes except a narrowly documented task003 interface correction approved by the orchestrator.
- Session reconnect/history, file transfer, port forwarding, terminal recording or multi-node limits.
- Main/static/deployment wiring and three-host E2E.

## Acceptance Criteria

The task is ready for review when:

- A real WebSocket integration test proves bidirectional bytes and resize through a fake PTY.
- Authentication, ownership, Origin, online and concurrency constraints fail closed.
- Every tested termination path releases PTY and capacity.
- Payload/logging limits are enforced.
- Required tests/builds pass or exact environmental failure is documented.
