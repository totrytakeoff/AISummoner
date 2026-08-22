---
task_id: task008-lifecycle
type: workstream_plan
status: ready_for_implementation
from: planner
to: lifecycle_coder
revision: 0
requires_review: true
---

# Task008 Lifecycle Workstream

## Objective

Make Device unpair the single authorization linearization point across SQLite,
Tunnel publication, Terminal sessions and every Agent Session/Turn/SSE surface.
This workstream does not wire the Server process or edit deployment/static
assets.

## Owned Files

- `internal/devicegate/` (new)
- `internal/device/` and focused tests
- `internal/tunnel/manager.go`, `internal/tunnel/server.go` and focused tests
- `internal/store/devices.go`, `internal/store/agent.go` and focused tests
- `internal/agent/service.go`, `internal/agent/hub.go` and focused tests
- `docs/agent_context/tasks/task008/lifecycle_summary.md`

Do not edit `cmd/`, `internal/config/`, `internal/app/`, `internal/staticweb/`,
`deploy/`, Web files or root operational files. Do not change approved wire
formats, SSH/PTTY behavior, approval policy or OpenCode behavior.

## Required Implementation

1. Add a context-aware keyed per-Device gate and inject the same instance into
   Gateway and Device Service.
2. Move Gateway's authoritative `RegisterDevice` call inside that gate. Keep
   the gate through `server.authenticated`, any unowned `pairing.offered`,
   deadline clearing and `Manager.Register`. On any offer/write/deadline error,
   do not publish. Preserve message ordering expected by the Remote Client.
   Gateway shutdown must reject new handlers, cancel and join both pre-auth and
   authenticated hijacked WebSocket handlers with Add/Wait linearized; public
   `http.Server.Shutdown` does not own hijacked connections.
3. Add an exact-current Manager detach boundary. While the Device gate is
   held it must remove the current mapping and synchronously mark that exact
   Connection logically closed/close its `Done` using memory-only operations,
   then return an idempotent cleanup handle. It must not perform a potentially
   blocking socket/yamux close while the Device gate is held. Run and join the
   returned exact-connection cleanup after unlocking, under the same bounded
   post-commit cleanup budget as Terminal and Agent. Preserve newest-wins and
   ensure late cleanup can never touch a replacement Connection. A convenience
   `InvalidateDevice` may remain for callers that do not need split detach/join.
   Apply the same split to newest-wins replacement: `Manager.Register` may
   install and logically close the replaced Connection inside the lifecycle
   gate, but it must not wait there for the replaced Connection's network
   cleanup. Every initiated cleanup remains owned by an idempotent completion
   signal that the old handler/Manager shutdown can join. Manager shutdown
   atomically rejects later registration, initiates logical close for every
   current/retiring Connection before joining any one cleanup, and joins the
   complete retiring set; a Register racing the shutdown snapshot cannot
   publish afterward.
4. In one SQLite transaction, owner-check and clear the Device owner, consume
   active pairing codes, select every Session belonging to the old owner and
   Device, set them to internal terminal `revoked`, and terminalize all
   pending/started tools with the static reason `device unpaired`. Return the
   deterministically ordered revoked Session IDs. Roll back all four effects
   on any error.
5. Apply the current-owner/non-revoked predicate to every owner-facing Agent
   read and write. Atomic mutations must include the predicate in SQL, not rely
   on an earlier lookup. Re-pairing the Device to the same user must not revive
   old Sessions or `full_access`.
6. Add per-Turn completion signals and a process-lifetime revoked Session set.
   Split Agent invalidation into synchronous
   `MarkDeviceRevoked(deviceID, sessionIDs)` and joined
   `InvalidateDevice(ctx, deviceID, sessionIDs) error`. The mark runs after
   commit while the Device gate is held, mutates runtime admission state and
   synchronously initiates cancellation of matching running Turns, but never
   waits or performs network I/O. Joined invalidation later closes all affected Session
   subscribers, cancels matching running/pending work and waits without
   holding the Service mutex. Turn finalizers must not overwrite `revoked` or
   publish a misleading successful terminal event.
7. Device Service holds the gate through the transaction, Agent runtime mark
   and the Tunnel's pure-memory detach/cancel initiation, then unlocks before
   joining the detached Tunnel cleanup, Terminal and Agent. Post-commit cleanup
   uses a fresh bounded context and is idempotent; request cancellation cannot
   skip it. A blocked network close may exhaust the cleanup budget but cannot
   keep the Device gate or restore the detached mapping.

## Deterministic Tests

- Gateway-first: candidate publication is held before Register; Unpair cannot
  commit until publication completes, then detaches both old/current live
  resources. A subsequent connection is unowned and receives one fresh code.
- Unpair-first: a proof-complete candidate waits at the gate, rereads the
  committed unowned record, sends `authenticated` then a fresh pairing offer,
  and only then becomes online. Offer/write failure never publishes.
- Manager invalidation racing a replacement never closes/removes the newer
  Connection incorrectly.
- A detached Connection whose network close is deliberately blocked makes the
  Manager offline immediately and does not prevent a waiting same-Device gate
  holder from publishing a replacement. The old cleanup is bounded/joined and
  never closes that replacement.
- A newest-wins replacement is published and releases the Device gate even
  when the replaced Connection's network close is blocked; the old cleanup
  remains owned/joinable and cannot delay the new heartbeat lifecycle.
- Manager shutdown racing publication rejects/cleans the candidate and leaves
  no online mapping. With several Connections, a deliberately blocked first
  cleanup does not prevent logical cancellation of every other Connection
  before the join begins.
- Gateway Close with an idle pre-auth WebSocket and an authenticated WebSocket
  closes both, releases pre-auth admission, joins every handler, and cannot
  race a new handler Add after its Wait begins.
- StartTurn lookup-first/commit-first and BeginTurn-first/unpair-second
  interleavings use channels, not sleeps. Verify adapter admission, child
  cancellation, joined completion, tool terminal state and no state overwrite.
- Idle SSE closes on unpair. Snapshot, Subscribe, Decide and every mutation
  hide revoked Sessions. Re-pair same user cannot recover an old idle or
  `full_access` Session.
- Store fault injection proves transaction rollback for owner, pairing code,
  Sessions and Tools.
- Gate waiter cancellation/key cleanup and the critical interleavings pass
  focused `-race` within the ASD resource limits.

## Handoff

Write `lifecycle_summary.md` with exact exported signatures, changed SQL
predicates, linearization points, deterministic test names and fresh commands.
Set it to `ready_for_integration`; do not mark parent Task008 complete.
