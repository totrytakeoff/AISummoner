---
task_id: task002
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 002 Plan: Device Identity And WSS/yamux Control Tunnel

## Status

Ready for implementation after task001 review approval.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

Task001 provides storage, device registration primitives and the HTTP Server skeleton. Task002 adds the authenticated outbound Remote connection and live Connection Manager, but deliberately stops before SSH/PTY.

## Goal

Deliver a Linux Remote Client that creates/persists Device Identity, connects to the Server through WebSocket/yamux, performs challenge/signature authentication, receives a pairing code, maintains heartbeat/reconnect, and drives Device online state.

## Relevant Files

- `cmd/aisummoner-client/`
- `internal/protocol/`
- `internal/identity/`
- `internal/wsstream/`
- `internal/tunnel/`
- `internal/device/` only for the online-state adapter needed by existing API
- `cmd/aisummoner-server/main.go` only for Tunnel route/wiring
- `go.mod`, `go.sum` only for coder/websocket and yamux dependencies
- `.env.example`, `Makefile` only for client/tunnel settings and targets
- `docs/agent_context/tasks/task002/summary.md`

## Required Behavior

- Client first start writes an Ed25519 private key at mode 0600 and stable metadata; later starts reuse it.
- Device ID is deterministically derived exactly as baseline specifies.
- Client refuses effective UID 0 unless an explicit development-only flag is set.
- Remote connects to `/api/v1/tunnel` using `ws` only in explicit dev mode and `wss` otherwise.
- WebSocket is safely exposed as a byte stream, then carries a yamux Client session.
- First and only Client-opened stream is `control` with a bounded versioned header.
- Server challenge uses a 32-byte random nonce and verifies the exact domain-separated signature.
- Server registers/refreshes device metadata only after proof succeeds.
- Server creates a connection-scoped Ed25519 SSH Client key and sends only its public key in `server.authenticated`.
- Unowned authenticated devices receive a ten-minute one-time pairing code over control; Client prints it without logging secrets.
- Heartbeat is every five seconds; 15-second timeout marks offline and closes connection.
- Duplicate authenticated Device connection uses newest-wins and closes the old session.
- Client reconnects with 1/2/4/8/15-second jittered backoff and resets after stable online time.
- Device HTTP responses obtain Online state from live Connection Manager.
- Unknown version/type, malformed/oversized control frames and pre-auth timeout fail closed.

## Required Changes

- Implement a length-prefixed JSON control codec with 64 KiB maximum.
- Implement WebSocket stream adapter close/deadline behavior and concurrent-write safety.
- Implement Server registry API for online lookup, SSH public/private connection credential access by later tasks, opening server-side streams, replacement and removal.
- Inject existing task001 store/pairing services rather than duplicating them.
- Add deterministic unit/integration tests using `httptest`, in-memory temp dirs and short configurable timers.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/protocol ./internal/identity ./internal/wsstream ./internal/tunnel ./internal/device
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

## Documentation Requirements

- Write `docs/agent_context/tasks/task002/summary.md` with exact commands and results.
- Keep `.env.example` aligned with actual client/server development TLS options.

## Out Of Scope

- SSH Server/Client negotiation, exec, PTY or Terminal WebSocket.
- Browser WebUI.
- Agent/OpenCode.
- Production proxy/TLS certificate deployment.
- Session recovery after Tunnel disconnect.

## Acceptance Criteria

The task is ready for review when:

- A fresh Client and Server complete the signed handshake and the persisted Store contains the verified device.
- Tampered signatures, mismatched Device IDs and public keys, bad versions, oversized frames and authentication timeout are tested and rejected.
- Pairing code is delivered only after authentication and can be claimed using task001 API.
- Heartbeat online/offline and newest-wins behavior are tested.
- Reconnect is bounded/cancellable and private-key file permissions are tested.
- Verification commands pass on ASD-Host or exact environmental failures are recorded.
- No SSH/Terminal/Agent scope was added.
