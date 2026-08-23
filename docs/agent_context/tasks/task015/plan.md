---
task_id: task015
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 015 Plan: Remote Core Daemon And Private IPC

## Status

Ready for implementation. This is the first half of the user-approved Remote
Client Qt refactor. Task016 will consume this frozen IPC to build the GUI and
GUI AppImage.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

The MVP Remote Client combines argument parsing, Device identity, Tunnel,
Embedded SSHD, process signal handling and pairing stdout in one command. The
CLI/AppImage proves the data plane but cannot safely support a GUI: there is no
long-lived local control object, no state snapshot, no sanitized event feed and
no way to pause/reconnect without killing the process.

The user chose Qt 6 for the GUI and moved Remote Client work ahead of the
Controller refactor. ADR-0004 still requires a daemon/UI split so GUI exit does
not terminate the reliable Tunnel core.

## Goal

Create a reusable Remote Core controller and an authenticated same-UID Unix
socket daemon API. Preserve the existing `start` behavior while adding a
`daemon` mode that Task016's Qt GUI can query and control.

## Relevant Files

- `cmd/aisummoner-client/main.go`, `main_test.go`
- `internal/tunnel/client.go`, focused client tests
- new `internal/remoteclient/`
- new `internal/clientipc/`
- `deploy/aisummoner-client.service`
- `README.md` and Alpha context documents

No Server, Browser, Agent or public Tunnel wire-protocol change is authorized.

## Required Behavior

### Remote Core

- Load/create the existing private Device identity and reuse the existing
  Ed25519 SSH signer/Embedded SSHD path.
- Own one reusable Tunnel client and expose an immutable `Snapshot` containing:
  Device ID/name, client version, configured Server origin, connection phase,
  timestamps/retry deadline, current pairing offer when valid, active generic
  SSH control-session count and a safe error category.
- Connection phases are finite and explicit: `starting`, `connecting`,
  `online`, `retrying`, `paused`, `stopped`, `error`.
- `Pause(ctx)` synchronously cancels and joins Tunnel/stream handlers, disables
  automatic reconnect and only then publishes `paused`.
- `Resume()` re-enables a single connection loop without duplicating workers.
- `RefreshPairing(ctx)` is available only while an unexpired/expired pairing
  offer exists; it joinedly cycles the Tunnel so the Server replaces the prior
  active code. It must warn through the fixed UI result that active sessions
  will close. A paired Device with no offer must reject refresh.
- Parent cancellation joinedly closes the current Tunnel and transitions to
  `stopped`.
- Existing `start` remains a compatible foreground command that prints pairing
  codes only to stdout and keeps structured stderr logs free of codes.

### Tunnel observations

- Add bounded, synchronous callbacks for connection phase and generic SSH
  stream opened/closed. Callbacks carry enums/count deltas and safe retry/error
  categories, not endpoint values, commands, stream bytes or credentials.
- Default nil callbacks preserve current behavior.
- Callback ordering is deterministic enough for the Core to publish
  connecting→online/retrying and never report a negative active count.
- Do not change protocol version or claim to distinguish Terminal from Agent in
  this task; current stream header only exposes generic SSH.

### Sanitized event ring

- Maintain at most 200 monotonically sequenced events in memory.
- Events use fixed summaries for daemon start/stop, connecting, online,
  retrying, pause/resume, pairing available/expired and control-session
  open/close.
- Events never contain pairing code, Server URL, command/cwd, Terminal/Agent
  content, SSH material, credential, raw transport error or request payload.

### Private local IPC

- `daemon` listens on an explicit/default Unix socket inside the mode-0700
  client data directory. Final socket mode is exactly 0600.
- Refuse symlink/non-socket stale paths and never remove a path not owned by the
  daemon UID. Remove only the exact owned socket on joined shutdown.
- Verify Linux `SO_PEERCRED` UID equals daemon effective UID before reading a
  request. Reject root/other-user peers even when filesystem permissions were
  weakened externally.
- Use newline-delimited JSON v1 with a 64 KiB request/response bound, exact
  fields, one request per connection, bounded deadlines and at most 8 concurrent
  handlers.
- Exact methods: `status.get`, `events.list`, `daemon.pause`, `daemon.resume`,
  `pairing.refresh`.
- `events.list` accepts a non-negative `after_sequence` and limit 1..200.
- Responses use typed fixed error codes/messages. Only `status.get` may return
  the current pairing code, and only across the verified local socket.

### CLI composition

- Add `daemon --server ... [--data-dir ...] [--socket ...]` with the same TLS,
  loopback development and root restrictions as `start`.
- Add bounded local `status`, `pause`, `resume`, `refresh-pairing` commands for
  deterministic testing and headless recovery; they take only data-dir/socket
  location and never Server credentials.
- Update systemd to use daemon mode. Its stderr remains structured non-secret
  logging; pairing stdout file is no longer the GUI contract, but may be kept
  for one documented compatibility release if `start` is used.

## Security And Lifecycle Invariants

- Remote remains outbound-only and opens no TCP listener.
- GUI/IPC never receives or serializes the Device private key.
- The pairing code is excluded from events, logs and errors.
- Pause/shutdown waits for all accepted streams and IPC handlers without
  holding state/event locks.
- No callback or socket write may block Tunnel cleanup indefinitely.
- Root remains refused outside explicit loopback development; the daemon socket
  does not create a root bypass.

## Required Tests

- Core state transition table, snapshot defensive copies and pairing expiry.
- Pause joins a blocked fake Tunnel; resume starts exactly one replacement;
  rapid pause/resume/refresh/cancel has no leaked worker or stale transition.
- Stream open/close concurrency never produces negative count; shutdown waits.
- Event ring capacity/sequence/cursor and mutation proof that no secret sentinel
  appears in serialized events/errors/log output.
- Real Unix socket mode/owner, same-UID accepted, forged/wrong peer seam denied,
  stale regular file/symlink refusal and exact cleanup.
- Oversized, malformed, duplicate/unknown field/method, invalid cursor/limit,
  deadline and concurrency-cap IPC cases.
- CLI compatibility, root gate, daemon flag validation and pairing stdout only
  in legacy `start`.
- Existing Tunnel client package tests and race coverage.

## Verification

Focused first, then merged under the existing resource gate:

```bash
GOMAXPROCS=2 go test -count=1 -p 1 ./internal/remoteclient ./internal/clientipc ./internal/tunnel ./cmd/aisummoner-client
GOMAXPROCS=2 go test -count=10 -p 1 ./internal/remoteclient ./internal/clientipc
GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/remoteclient ./internal/clientipc ./internal/tunnel ./cmd/aisummoner-client
GOMAXPROCS=2 go test -count=1 -p 2 ./...
GOMAXPROCS=2 go vet ./...
GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

## Documentation Requirements

- Update CLI/systemd/IPC usage without presenting IPC as remote-access API.
- Record schema and exact secret exclusions for Task016.
- Write `summary.md` with exact commands, failures and hashes. Do not modify
  `review.md`.

## Out Of Scope

- Qt source, CMake, GUI launch/autostart and GUI AppImage (Task016).
- Pairing success notification or Terminal-versus-Agent purpose in the public
  Tunnel protocol.
- Reset identity, persistent permission policy or update installation.
- Server/Browser changes, Controller workspace and Agent Runtime adapters.

## Acceptance Criteria

The task is ready for review when the same-user private IPC can observe a real
Core, pause/join it, resume exactly once, refresh an offered pairing code and
read a bounded sanitized event history, while legacy `start` and all existing
Tunnel/SSH behavior remain green.
