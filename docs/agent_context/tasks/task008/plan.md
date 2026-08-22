---
task_id: task008
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 2
requires_review: true
---

# Task 008 Plan: Full Server Wiring, Static Web, Deployment And Service Lifecycle

## Status

Ready for implementation only after tasks003-007 are approved. This is the single integration owner for command wiring, route composition, static embedding, configuration completion and deploy assets.

## Owner

Integration Implementation Agent.

## Reviewer

Independent Integration Reviewer Agent.

## Implementation Workstreams And Merge Gate

Task008 may be implemented by two agents in parallel, but it has one final
integration owner and one final independent review:

1. **Lifecycle workstream** owns only the Device lifecycle gate, Tunnel
   publication/invalidation, the atomic SQLite unpair/revocation transaction,
   Device Service cleanup orchestration, and Agent runtime invalidation. It
   writes focused interleaving tests and freezes the public boundaries listed
   below before the composition workstream consumes them.
2. **Composition workstream** owns configuration, static serving, process
   wiring, route dispatch, health/shutdown, deployment assets and operational
   documentation. It may prepare disjoint config/static/deploy code in
   parallel, but must not guess or edit lifecycle-owned implementations.
3. The composition owner performs the final in-process composition tests only
   after the lifecycle workstream reports a focused green checkpoint. A single
   Task008 summary covers both workstreams. Neither implementation agent may
   self-approve the combined Task008 result.

The workstreams must not overlap files. Lifecycle owns
`internal/devicegate/`, `internal/device/`, the explicitly authorized Tunnel,
Store and Agent files, and their focused tests. Composition owns `cmd/`,
`internal/config/`, `internal/app/`, `internal/staticweb/`, `deploy/`, Web embed
glue and root operational files.

## Frozen Lifecycle Boundaries

Names may change only with an explicit planner update; their capabilities and
linearization semantics may not:

- One shared context-aware keyed gate exposes
  `LockDevice(context.Context, deviceID) (unlock func(), err error)`. Gateway
  and Device Service receive the same instance. Waiting is cancelable; unlock
  is idempotent; unused keys are removed.
- Store unpair returns the exact revoked Agent Session IDs from the same
  transaction that clears ownership and consumes pairing codes, for example
  `UnpairResult{RevokedAgentSessionIDs []string}`. A transaction failure
  exposes no partial ownership, code, Session or Tool mutation.
- `Manager.InvalidateDevice(deviceID)` first removes exactly the current map
  entry under the Manager mutex, then closes that detached Connection outside
  the mutex. It never performs `Get -> Close -> Remove`, never removes a newer
  replacement, and never restores online state after a close error.
- Agent Service exposes two explicit phases. Synchronous
  `MarkDeviceRevoked(deviceID, revokedSessionIDs)` is called after the database
  commit while the Device gate is still held; it installs runtime tombstones,
  synchronously initiates cancellation for already-running matching Turns, and
  closes the Store-lookup-to-in-memory-admission/re-execution races without
  waiting or doing network I/O.
  Joined, idempotent `InvalidateDevice(ctx, deviceID, revokedSessionIDs)` runs
  after the Device gate is released; it closes SSE for idle and active revoked
  Sessions, cancels matching Turns/approval waits, and waits for per-Turn
  `done` signals without holding the Service mutex. Cancellation finalizers
  treat authorization revocation as terminal and never persist `idle`/`failed`
  over `revoked`.
- Terminal keeps its approved generation-protected, joined
  `CancelDevice(deviceID)` boundary. Composition adapts Task003 through a
  closure; it must not change Task003 return types to satisfy Terminal.
- Device Service keeps the existing browser-facing `Unpair(... ) error`
  behavior. Internally it holds the shared gate through the database commit,
  Agent runtime mark and Manager detachment, then releases the gate before
  bounded Terminal/Agent joins using a fresh cleanup context. The caller's
  cancellation after commit cannot skip revocation cleanup or restore
  authority.

All Agent Store owner-facing reads and writes must share one semantic
predicate: product Session user matches, Session is not `revoked`, and the
referenced Device is still owned by that user. Internal canceled-Turn cleanup
may distinguish revocation from an infrastructure write failure, but HTTP/SSE
callers continue to see the normal not-found/closed surface rather than a new
authorization oracle.

## Context

Previous tasks deliberately expose standalone packages/handlers to avoid concurrent changes to `cmd/` and the shared API stack. This task composes the approved Auth/Device/Tunnel/SSH/Terminal/Agent/OpenCode pieces into one Server process and one Remote Client, embeds the production Web build, and provides a reproducible TLS-capable deployment example.

## Goal

Produce runnable Server and Linux Remote Client binaries plus static WebUI and sidecar/deployment assets, with correct startup/shutdown, routing, configuration, unpair invalidation and health behavior.

## Relevant Files

- `cmd/aisummoner-server/`
- `cmd/aisummoner-client/`
- `internal/config/`
- a narrow composition/static package if needed (`internal/app/`, `internal/staticweb/` or `web/embed.go`)
- `internal/device/` only for the approved unpair lifecycle hook
- `internal/tunnel/manager.go`, `internal/tunnel/server.go` and focused tests only for the approved Device lifecycle/publication gate and exact-device invalidation
- `internal/store/devices.go`, `internal/store/agent.go` and focused tests only for atomic unpair/Agent revocation and current-owner authorization
- `internal/agent/service.go`, `internal/agent/hub.go` and focused tests only for joined Device invalidation and revoked-session subscriber closure
- `web/` only for production embed glue/build output policy, not UI redesign
- `deploy/`
- root `Makefile`, `.gitignore`, `.env.example`, `README.md`
- `docs/agent_context/tasks/task008/summary.md`

Do not alter approved package state machines/protocols merely to simplify wiring. Report incompatible interfaces to the orchestrator.

## Server Composition

- Initialize validated config, private data/workspace directories, SQLite migrations and admin bootstrap.
- Construct Auth, Pairing, Device, Tunnel Manager/Gateway, strict SSH client opener, Terminal Handler, Agent Store/Service/Fake Adapter or OpenCode Adapter, OpenCode bridge and browser API handlers.
- Select Agent Adapter only from explicit `fake` or `opencode`; unknown values fail startup.
- Mount exact routes without exposing internal bridge on the public listener. Public middleware must preserve WebSocket/SSE capabilities and request IDs.
- Browser JSON routes use Task001 standard envelope. Terminal GET requires its own Origin enforcement. Tunnel stays unauthenticated until Device challenge, never browser auth.
- Serve immutable hashed assets with long cache and `index.html`/SPA fallback with no-cache. API/Terminal/Tunnel misses must never fall through to HTML.
- `/healthz` reports Server/SQLite readiness without secrets. Add an authenticated or loopback-only detailed OpenCode diagnostic only if already approved; public health must not claim model availability.

## Remote Client Composition

- Wire task003 Embedded SSH Server as the task002 SSH stream handler using Device Identity host key and current Tunnel-provided ephemeral client public key.
- Each incoming stream gets only the current authenticated connection scope and closes independently.
- Signal cancellation closes control Tunnel, all active SSH connections and child processes.
- Preserve root refusal and secure WSS defaults. Add certificate/CA flags only if needed for the reproducible test deployment; any insecure-skip-verify flag must require explicit `--dev` and be rejected otherwise.
- `--dev` authorizes plaintext only to a literal loopback Server address. `http`/`ws` URLs using a non-loopback address or a hostname alias such as `localhost` are rejected, because plaintext control authentication otherwise permits a network attacker to supply the SSH client key and reach the Remote data plane. This narrow Task002-boundary hardening is explicitly authorized in `internal/tunnel/client.go` plus focused tests as part of Task008 integration.
- Remote Tunnel WebSocket handshakes never follow HTTP redirects. `NewClient` clones the injected/default `http.Client`, preserves its transport, jar and timeout, overrides only redirect handling with `http.ErrUseLastResponse`, and never mutates the caller's client. This pins Device proof and the connection-scoped SSH client key to the configured Server origin. Real redirect fixtures cover loopback development and TLS paths and prove the redirect target never receives an upgrade/proof. This second narrow Task002-boundary hardening is explicitly authorized in the same files.

## Unpair And Lifecycle Invalidation

- A successful owner-authorized unpair atomically removes ownership, consumes active pairing codes and persistently revokes every existing Agent Session for that Device in the same SQLite transaction. `revoked` is an internal terminal state: it cannot be restarted or overwritten by a canceled Turn's cleanup, and old `full_access` authority cannot revive if the same administrator later re-pairs the Device. Pending/started tools become terminal failed with a static non-secret reason.
- Every Agent read/mutation boundary revalidates both `agent_sessions.user_id` and the Device's current `owner_user_id`, and rejects revoked sessions. This includes Snapshot, Subscribe, Start/Begin Turn, message/tool persistence and decisions. Existing SSE subscribers for returned revoked Session IDs are closed.
- Gateway authentication/publication and Device unpair share one context-aware per-Device lifecycle gate. Gateway acquires it before `RegisterDevice` reads the authoritative owner and holds it through any fresh pairing offer, successful `server.authenticated`/`pairing.offered` writes and `Manager.Register`. An unowned connection is not published until its fresh pairing code was delivered. Unpair holds the same gate through the transaction and atomic in-memory detachment. Therefore either the candidate connection publishes first and unpair removes it, or unpair commits first and the candidate rereads `owner_user_id IS NULL` and delivers a fresh code before publication.
- Add a narrow split Manager boundary: while the Device lifecycle gate is held,
  atomically remove exactly the current mapping and synchronously mark that
  Connection logically closed/close its `Done` with memory-only operations,
  returning an idempotent exact-connection cleanup handle. Do not run arbitrary
  socket/yamux `Close` while holding the Device gate, and never implement unpair
  as `Get -> Close -> Remove`, which races newest-wins replacement.
- After commit and after releasing the Device gate, run/join the detached
  Tunnel cleanup, call Task005's generation-protected joined `CancelDevice`,
  and cancel/join all running/pending Agent Turns plus close idle Session SSE
  subscribers for the revoked IDs under one fresh bounded cleanup budget. A
  slow old Tunnel cleanup may time out but cannot retain the gate, restore the
  mapping or affect a replacement. Expose only the smallest lifecycle
  interfaces between Device Service and these registries.
- Newest-wins registration follows the same rule: it may logically close the
  replaced Connection while the lifecycle gate is held, but waiting for the
  old socket/yamux cleanup must occur outside that gate. Cleanup must remain
  owned and joinable; it cannot be an untracked fire-and-forget goroutine or
  delay the newly published Connection's heartbeat path.
- Manager shutdown is a publication barrier: atomically enter a closed state,
  reject and clean any later registration, synchronously initiate logical
  close for all current/retiring Connections, then join their network cleanup.
  It must not snapshot current mappings and allow a racing Register to escape,
  nor block initiation for later Connections while joining the first one.
- The database commit is the irreversible authorization linearization point. A canceled HTTP request after commit or a transport cleanup error never restores ownership/session authority. Use a fresh bounded cleanup context, make all invalidation operations idempotent, and keep cleanup progressing/retryable after a caller deadline; log/audit only safe IDs/categories.
- New Remote authentication after unpair is unowned and receives a fresh one-time pairing code.
- Shutdown order: stop accepting browser/tunnel work; cancel Agent turns and OpenCode mappings; close Terminal/SSH; close all tunnels; shut down HTTP; close DB. Bound total graceful shutdown.
- Tunnel Gateway owns and joins pre-auth as well as authenticated hijacked
  WebSocket handlers; `http.Server.Shutdown` alone is not treated as their
  lifecycle owner. Handler admission and Gateway Close/Wait are linearized so
  shutdown cannot miss a late Add.
- All goroutines/subscribers/child processes have a cancellation owner.

## Configuration

Validate at startup and never log values for:

- existing Base URL/listen/data/admin/session/pairing settings;
- `AISUMMONER_AGENT_ADAPTER`;
- OpenCode loopback URL, username, password, model;
- Agent bridge secret (minimum 32 bytes for OpenCode mode);
- private Agent workspace root or safe default under data dir;
- internal OpenCode bridge loopback listen address if separate.

OpenCode mode requires every OpenCode/bridge value. Fake mode must not require OpenCode credentials. Production Base URL remains HTTPS-only; public Server may listen plain HTTP only behind the included same-host TLS proxy, clearly documented.

## Static Web Build

- Production build is deterministic from committed `package-lock.json` and produces assets embedded or copied into the Server image.
- `go test ./...` must work from a clean source checkout without requiring an already-generated untracked directory. Use a tracked minimal embed placeholder or a generated package strategy that does not hide missing build steps.
- Production Docker build runs Web build before Go build and embeds the resulting assets.

## Deployment Assets

Provide:

- multi-stage `deploy/Dockerfile` for Web build and static Go Server binary;
- `deploy/compose.yaml` with Server data volume, loopback/private OpenCode sidecar relationship and Caddy TLS proxy example;
- `deploy/Caddyfile` using an environment-provided hostname and reverse proxy that supports WebSocket/SSE;
- health checks, restart policy, read-only/rootless settings where practical without blocking SQLite volume writes;
- example systemd unit or documented direct binary service for Linux Remote Client;
- `.env.example` placeholders only, no real host/password/token/key/IP secret;
- operational README for initial bootstrap, secret generation, OpenCode auth/model selection, pairing and shutdown.

Do not install services or modify remote hosts in this task; task010 performs controlled deployment.

## Required Tests

- Config fake/OpenCode validation, loopback enforcement and secret redaction.
- Full in-process Server route composition: Auth/Device JSON, Tunnel WebSocket capability, Terminal WebSocket capability, Agent SSE flushing, static SPA fallback and API 404 separation.
- Remote client composition handles an authenticated SSH stream and shuts down all children.
- Successful unpair closes current Tunnel/Terminal and cancels pending/running Agent work; new connection becomes unowned.
- Forced Gateway/unpair interleavings cover both orders without sleeps: publication-first is detached by unpair; unpair-first forces the authenticated candidate to reread unowned state, deliver a fresh pairing code before Manager publication, and never publish on offer/write failure.
- A deliberately blocked detached-Tunnel network close proves `IsOnline`
  becomes false immediately, a waiting same-Device authentication can proceed,
  post-commit cleanup remains bounded, and late old cleanup cannot close the
  replacement.
- Forced Agent/unpair interleavings cover StartTurn before and after the revocation commit, running cancellation/join, SSE closure, pending/started tool terminalization, transaction rollback, and re-pairing the same Device to the same administrator without reviving an old idle or `full_access` Session.
- Adapter selection and OpenCode-unavailable startup/runtime behavior.
- Static handler cache/fallback/path traversal behavior.
- Graceful shutdown does not hang with open SSE/WebSocket/Tunnel fixtures.
- `docker compose config` and Docker image build when environment/resources permit.

## Verification

Before each heavy command, check local/ASD memory and run sequentially:

```bash
GOMAXPROCS=2 go test -p 2 ./internal/... ./cmd/...
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
npm --prefix web ci
npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
docker compose -f deploy/compose.yaml config
docker build -f deploy/Dockerfile .
```

Docker build may run on ASD-Host if local disk/memory is unsafe, but do not overlap it with Node/Go/race work.

## Documentation Requirements

- Write `docs/agent_context/tasks/task008/summary.md` with exact wiring, ports, shutdown behavior, build evidence and deviations.
- Update README and `.env.example` in the same change for every new user-facing config/command.

## Out Of Scope

- Actual ASD/lzr deployment or changing their nginx/firewall/systemd state.
- Public production readiness, backup automation, secret manager, horizontal scaling or credential copying.
- New product features beyond MVP-0.

## Acceptance Criteria

The task is ready for review when:

- One Server binary exposes every MVP browser/device route and embeds the WebUI.
- One Remote binary accepts strict Embedded SSH streams over its outbound Tunnel.
- Fake Adapter mode completes without any OpenCode credentials; OpenCode mode is fail-closed and loopback-only.
- Unpair and graceful shutdown invalidate all affected live resources.
- Clean builds/tests/Web build/Compose validation pass within resource limits; Docker result is recorded exactly.
