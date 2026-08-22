---
task_id: task008-lifecycle
type: summary
status: ready_for_integration
from: lifecycle_coder
to: task008-integration-coder
revision: 0
review_required: true
---

# Task008 Lifecycle Workstream Summary

## Scope and Files Changed

- `internal/devicegate/gate.go`, `gate_test.go` — added the shared, context-aware keyed Device lifecycle gate and deterministic cancellation/key-cleanup tests.
- `internal/tunnel/manager.go`, `manager_test.go` — added logical Connection close versus joined I/O cleanup, Manager-owned retired connections, closed admission, exact detach, replacement-safe cleanup, and joined shutdown.
- `internal/tunnel/server.go`, `lifecycle_test.go` — placed authenticated Device registration/publication inside the shared gate; preserved `authenticated` then optional `pairing.offered` then online ordering; added final context checks and joined Gateway handler/watcher shutdown.
- `internal/store/devices.go`, `store_test.go` — made Unpair one transaction that consumes codes, revokes Sessions, fails active tools, clears ownership, and returns sorted Session IDs; added rollback fault injection.
- `internal/store/agent.go`, `agent_test.go` — applied the canonical current-owner/non-revoked predicate to every owner-facing Agent read and mutation.
- `internal/agent/service.go`, `hub.go`, `service_test.go` — added process-lifetime runtime tombstones, synchronous cancellation initiation, joined invalidation, subscriber closure, per-Turn completion, and pre-executor revocation checks.
- `internal/device/service.go`, `service_test.go` — added lifecycle composition boundaries and gate-held commit/mark/detach followed by fresh-context, bounded, three-way joined cleanup.

No `cmd/`, configuration, app/static, Terminal, SSH, OpenCode, migration, baseline, ADR, Task008 parent summary, or Web file was changed.

## Exported Interfaces

```go
func devicegate.New() *devicegate.Gate
func (*devicegate.Gate) LockDevice(context.Context, string) (func(), error)

type store.UnpairResult struct {
    RevokedAgentSessionIDs []string
}
func (*store.Store) UnpairDevice(context.Context, string, string, time.Time) (store.UnpairResult, error)

func device.NewLifecycleService(device.LifecycleOptions) (*device.Service, error)
func (*device.Service) Unpair(context.Context, string, string, time.Time) error

func (*tunnel.Manager) Register(*tunnel.Connection) (replaced *tunnel.Connection, accepted bool)
func (*tunnel.Manager) DetachDevice(string) func() error
func (*tunnel.Manager) InvalidateDevice(string)

func (*agent.Service) MarkDeviceRevoked(string, []string)
func (*agent.Service) InvalidateDevice(context.Context, string, []string) error
```

`tunnel.GatewayOptions` now has `DeviceGate DeviceLifecycleGate`. Production composition must inject the same `*devicegate.Gate` into Gateway and `device.NewLifecycleService`; the Gateway-only default gate is for safe standalone construction and does not connect Device Unpair. `device.NewService` remains a List/Get compatibility constructor and returns fixed `device.ErrLifecycleUnavailable` from Unpair.

`Manager.Register` now reports rejection after Manager shutdown. `Manager.DetachDevice` synchronously removes and logically closes the exact current Connection, then returns an idempotent I/O-cleanup join handle. Device Service calls the handle outside its shared gate. `Manager.InvalidateDevice` remains the synchronous standalone convenience wrapper.

## Persistence and Authorization

`Store.UnpairDevice` performs these operations in one SQLite transaction:

1. Authorize exact `devices.id + owner_user_id`.
2. Select all old-owner/device Agent Session IDs ordered by ID.
3. Consume every active pairing code for the Device.
4. Set every matching Agent Session to internal terminal state `revoked`.
5. Set every matching pending/started tool to `failed`, clear exit code, set `output_excerpt = 'device unpaired'`, and set `completed_at`.
6. Clear `owner_user_id` and `paired_at`, then commit.

Every owner-facing Agent query/mutation now embeds one of these equivalent SQL predicates at its authoritative statement:

```sql
agent_sessions.user_id = ?
AND agent_sessions.state <> 'revoked'
AND EXISTS (
  SELECT 1 FROM devices
  WHERE devices.id = agent_sessions.device_id
  AND devices.owner_user_id = agent_sessions.user_id
)
```

Tool operations use an `EXISTS` join from `tool_calls.session_id` through `agent_sessions` to `devices` with the same user, non-revoked, and current-owner requirements. This covers Session/Snapshot/message/tool reads, state and external-session updates, BeginTurn, message/tool creation, Decide and approval upgrade, pending failure, tool start, and tool finish. Snapshot reads Session, messages, and tools in one read transaction. Re-pairing the Device to the same user cannot revive old Sessions or inherited `full_access`.

## Linearization and Lifecycle

- **Gateway first:** after proof verification, Gateway acquires the Device gate, rereads/registers the Device, sends `server.authenticated`, sends one fresh `pairing.offered` only when unowned, clears deadlines, rechecks auth context, then publishes through Manager. Any register/offer/write/deadline/context/Manager-close failure leaves the candidate unpublished.
- **Unpair first:** Device Service holds the same gate through the committed Store transaction, Agent runtime mark/cancel initiation, and exact Tunnel logical detach. A proof-complete candidate waits, then rereads the committed unowned record and cannot publish before its pairing offer.
- **Gate release:** arbitrary Tunnel/yamux/network close, Terminal cancellation, and joined Agent invalidation run only after the gate is released under a fresh bounded cleanup context. The result channel is capacity three, so workers finishing after a timeout cannot block while reporting.
- **Agent runtime:** `MarkDeviceRevoked` adds non-evicting process-lifetime tombstones and synchronously invokes matching Turn cancel functions before return. `execute` checks both Turn context and tombstone immediately before executor admission. `InvalidateDevice` closes affected SSE subscribers, cancels again idempotently, and joins each Turn's `done`; `done` is closed under `Service.mu` before running-map deletion.
- **Lock order:** the only nested Agent lock order is `Service.mu -> hub.mu`. Subscribe performs persistent authorization first, then atomically checks the runtime tombstone and subscribes under that order.
- **Shutdown:** Connection logical close is immediate while I/O cleanup is owned/joinable. Manager tracks active and retired Connections, rejects post-close registration, begins every close before waiting, and joins all owned cleanup. Gateway linearizes handler admission against Close, cancels, closes Manager, and joins pre-auth/authenticated handlers plus the extra-stream watcher. Concurrent Close callers share one completion boundary.

## Deterministic Tests Added or Strengthened

- Gate: `TestSameDeviceSerializesCanceledWaiterAndCleansKey`, `TestCanceledContextRacingAvailableTokenReturnsItAndCleansKey`, `TestDifferentDevicesProceedIndependently`.
- Store: `TestUnpairAtomicallyRevokesSessionsToolsAndCodes`, `TestUnpairRollbackRestoresAllFourEffects`, `TestRevokedSessionOwnerSurfaceAndMutationsStayHiddenAfterRepair`.
- Agent: `TestStartTurnLookupBeforeRevocationCannotReserve`, `TestMarkRevokedCancelsStartedToolBeforeExecutorAfterSameOwnerRepair`, `TestInvalidateDeviceClosesSSEAndJoinsRunningTurnWithoutFinalStateOverwrite`, `TestSubscribeLookupRaceAndPendingApprovalInvalidation`, `TestInvalidateDeviceTimeoutCanBeRetriedAndJoined`.
- Device: `TestLegacyServiceRejectsUnsafeDirectUnpair`, `TestUnpairCommitCancellationStillMarksDetachesThenUnlocksBeforeJoinedCleanup`, `TestUnpairFailureUnlocksWithoutInvalidation`, `TestUnpairCleanupTimeoutDoesNotStrandLateWorkerReports`.
- Tunnel/Manager: `TestGatewayFirstPublicationThenUnpairDetachesAndNextConnectionGetsFreshOffer`, `TestUnpairFirstCandidateRereadsUnownedSendsOfferThenPublishes`, `TestPairingOfferFailureNeverPublishesOrReplacesHealthyConnection`, `TestGatewayCloseJoinsBlockedHandlersAndRejectsNewAdmissions`, `TestManagerInvalidateDeviceDetachPreservesConcurrentReplacement`, `TestManagerReplacementIsNonBlockingAndCloseAllJoinsEveryRetiredConnection`, `TestManagerRejectsRegisterAfterCloseAndCallerCanJoinCandidate`, and `TestCanceledAuthenticationAfterDeadlineClearDoesNotPublish`.

The barriers establish both Gateway/Unpair orders, wire-before-publication, offer-before-online, offline-before-blocked Tunnel close, same-owner repair before releasing an old Turn, synchronous `ctx.Done` visibility when `MarkDeviceRevoked` returns, no executor admission through a repaired Tunnel, exact replacement preservation, cleanup sends after timeout, both-ready gate cancellation/token compensation, joined Gateway child watcher exit, and Add/Wait-safe shutdown.

## Verification

All Go work ran on ASD-Host in isolated staging `/tmp/aisummoner-task008-stage1.7zmz98` with `GOMAXPROCS=2` and serialized package testing. Local and remote SHA-256 hashes for all 15 owned Go source/test files matched after remote `gofmt`.

- First Stage 1 attempt: `GOMAXPROCS=2 go test -count=1 -p 1 ./internal/devicegate ./internal/store ./internal/agent ./internal/device ./internal/tunnel`
  - `store`, `agent`, and `device` passed.
  - `devicegate` failed because its private wait hook remained installed during the test's fresh acquire and closed an already-closed channel.
  - `tunnel` failed in `TestGatewayMissedHeartbeatTimesOutWithoutPeerCancellation` and `TestGatewayAuthenticatedNewestWinsAndOldCleanupKeepsNew` because those old tests assumed receipt of the authenticated frame meant Manager publication had already completed.
  - Fixed only the three test synchronization oracles; no production behavior was changed for these failures.
- Fresh Stage 1 retry: same command — PASS: `devicegate 0.003s`, `store 0.048s`, `agent 1.534s`, `device 0.024s`, `tunnel 1.007s`.
- First Stage 2 critical repeat: anchored lifecycle `-run` expression with `GOMAXPROCS=2 go test -count=10 -p 1 -timeout 360s` across the five packages.
  - `devicegate`, `store`, `agent`, and `device` passed.
  - Redundant `TestGatewayCloseJoinsAuthenticatedConnection` failed 4/10 because it repeated the same wire-before-publication assumption. It was removed as a weaker duplicate of the real-WebSocket blocked-gate, child-watcher, concurrent-Close oracle; production was unchanged.
- Corrected Stage 2 repeat: same anchored set without the removed weak test — PASS: `devicegate 0.007s`, `store 0.217s`, `agent 0.346s`, `device 0.209s`, `tunnel 0.235s`.
- Race: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 360s ./internal/devicegate ./internal/store ./internal/agent ./internal/device ./internal/tunnel` — PASS: `1.016s`, `1.595s`, `5.791s`, `1.034s`, `2.641s` respectively.
- Vet: `GOMAXPROCS=2 go vet ./internal/devicegate ./internal/store ./internal/agent ./internal/device ./internal/tunnel` — PASS, no output.
- Build: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client` — PASS, no output.
- Final `git diff --check` over lifecycle-owned files — PASS, no output.
- Final ASD resource check: `MemAvailable: 5478040 kB`, `SwapFree: 2062420 kB`; no residual `go`, `compile`, `link`, or `vet` process.

The first attempt to transfer staging with `rsync` failed because ASD-Host does not have `rsync`; the isolated tree was transferred successfully with `tar`. A later local sync-back command had one mistyped tar path and was rerun with the correct explicit file list before testing; hashes matched. Neither was a source or test failure.

## Integration Requirements and Residuals

- Task008 composition must create one shared `*devicegate.Gate`, inject it into Gateway and `device.NewLifecycleService`, and switch production Device HTTP Unpair away from the legacy List/Get-only constructor.
- Composition must account for the new `Manager.Register` accepted result and use `Manager.DetachDevice` through Device lifecycle injection; Gateway already handles rejected publication.
- Runtime revoked tombstones intentionally do not evict during a process lifetime. This closes same-owner repair TOCTOU windows at the cost of process-lifetime growth proportional to revoked Agent Sessions; this is an explicit single-node MVP residual.
- A bounded post-commit timeout can return while an uncooperative Tunnel/Terminal cleanup goroutine continues. Logical Tunnel detach and Agent cancel initiation have already occurred, late result sends cannot block, and cleanup remains idempotent/retryable. This does not claim hard-kill recovery.

## Deviations From Plan

- The approved lifecycle amendment split Tunnel logical detach from network close and added joined Manager/Gateway shutdown. This was required because arbitrary `Connection.Close` under the shared Device gate could otherwise block authentication and Unpair indefinitely.
- No parent Task008 composition, route wiring, config, migration, Terminal, SSH, OpenCode, Web, baseline, or ADR work was performed.
