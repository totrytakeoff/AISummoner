---
task_id: task002
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 2
review_required: true
---

# Task 002 Summary

## Files Changed

- `cmd/aisummoner-client/main.go` - added the Linux Remote Client entry point, root refusal, identity loading, explicit development flags, lifecycle signals, and pairing-code display.
- `cmd/aisummoner-server/main.go` - wired the Tunnel Gateway and live Connection Manager alongside the approved task001 HTTP API.
- `internal/protocol/control.go`, `control_test.go` - added the versioned 64 KiB length-prefixed JSON control codec, strict payload decoding, stream headers, and protocol tests.
- `internal/identity/identity.go`, `identity_test.go` - added stable Ed25519 identity generation/loading, deterministic Device ID derivation, protected metadata, PKCS#8 private-key persistence, and 0600 enforcement tests.
- `internal/wsstream/conn.go`, `conn_test.go` - added the binary WebSocket `net.Conn` adapter with serialized concurrent writes, deadlines, idempotent close, and loopback tests.
- `internal/tunnel/manager.go`, `manager_test.go` - added the in-memory authenticated Connection Manager, newest-wins replacement, exact-instance removal, online lookup, connection-scoped SSH signer access, and typed Server stream opening for task003.
- `internal/tunnel/server.go`, `client.go`, `client_test.go`, `integration_test.go` - added WSS/yamux setup, bounded pre-authentication, challenge/proof authentication, registration, pairing offers, heartbeats, timeout/offline behavior, reconnect backoff/jitter, stream acceptance, and full WebSocket/yamux integration coverage.
- `go.mod`, `go.sum` - added the approved `coder/websocket` and `hashicorp/yamux` dependencies and resolved transitive module metadata.
- `.env.example`, `Makefile` - documented Remote Client development transport/root flags and added separate Server/Client build/run targets.

## Behavior Changed

- First Client start creates a long-lived Ed25519 identity under its private data directory; restarts reuse it and reject a private key whose mode is not exactly 0600.
- The Client rejects effective UID 0 unless both `--dev` and `--allow-root-dev` are explicit, and rejects `ws/http` transport outside development mode.
- `/api/v1/tunnel` now upgrades to binary WebSocket, exposes it as a stream, and starts yamux. The first and only Client-opened stream is the bounded `control` stream.
- Server authentication validates the Device ID derived from the raw Ed25519 public key, issues a 32-byte nonce, and verifies the exact domain-separated signature before registration or online publication.
- Each authenticated Tunnel receives an in-memory connection ID and ephemeral Ed25519 SSH Client credential. Only the authorized public key is sent to Remote; the private signer remains in the Server-side Connection object for task003.
- An authenticated unowned Device receives a ten-minute one-time pairing offer through control, while the existing task001 pairing service remains authoritative for claim/consumption.
- Heartbeats are sent on the Server-provided interval, persisted as `last_seen_at`, acknowledged, and bounded by a Server read timeout. Disconnect/timeout removes only the exact current Connection.
- A second authenticated connection for the same Device atomically replaces and closes the old one. Client reconnect delay follows 1/2/4/8/15 seconds with approximately ±20% jitter and is cancelable.
- Device list/detail online state is now injected from the live Connection Manager without changing the approved task001 HTTP middleware or browser API contracts.

## Verification

- Environment check before remote work: `free -h && ssh ASD-Host 'free -h; df -h /tmp; go version'`
  Result: **PASS**; local machine reported 15 GiB available memory and 9.3 GiB free swap. ASD-Host reported 5.2 GiB available memory, approximately 2.0 GiB free swap, 3.8 GiB available in `/tmp`, and Go 1.24.4.
- Remote formatting/dependency preparation in an isolated `/tmp/aisummoner-task002.*` staging tree: `gofmt -w ...` and `GOMAXPROCS=2 go mod tidy`
  Result: **PASS**; `github.com/coder/websocket v1.8.14` and `github.com/hashicorp/yamux v0.1.2` resolved successfully.
- Focused integration test during initial convergence: `GOMAXPROCS=2 go test -p 2 ./internal/tunnel`
  Result: **PASS** in 0.134s. The revision-0 test exercised real `httptest` WebSocket/yamux authentication, pairing delivery, ongoing heartbeats, cancellation-driven disconnect, mismatch/tamper rejection, and authentication timeout. It did **not** independently prove missed-heartbeat timeout or an authenticated Gateway replacement; those claims are covered by revision 1 below.
- Required final remote test command: `GOMAXPROCS=2 go test -p 2 ./internal/protocol ./internal/identity ./internal/wsstream ./internal/tunnel ./internal/device`
  Result: **PASS**. Protocol, identity, wsstream, and tunnel packages passed; device compiled with `[no test files]` and its online adapter is exercised through the Tunnel integration/wiring.
- Required final remote build command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Final remote formatting assertion: `test -z "$(gofmt -l cmd/aisummoner-client cmd/aisummoner-server internal/protocol internal/identity internal/wsstream internal/tunnel)"`
  Result: **PASS**.

## Deviations From Plan

- The local workstation still has no Go toolchain, so all formatting, dependency resolution, focused tests, and builds ran in an isolated ASD-Host `/tmp` copy with concurrency limited to two.
- The requested verification list included `internal/device`; it has no package-local test file. Its unchanged adapter compiled and the Server wiring supplies the live manager.

## Known Issues / Follow-Up

- Task003 must install the Embedded SSHD `StreamHandler`, use `ClientSession.SSHClientPublicKey` for strict Remote authentication, and use the Server-side `Connection.SSHClientSigner` plus registered Device key for strict SSH negotiation.
- Revision 1 now covers the authenticated control-stream offer through the real task001 pairing claim API and owner-scoped `online: true` Device response in one process.
- No production TLS/Caddy deployment or real ASD-Host/lzr-host process test was run in task002; those belong to the later deployment and three-host E2E tasks.
- Revision 0 omitted race testing; revision 1 ran the affected Tunnel, WebSocket adapter, and client-command race suites serially after memory checks, as recorded below.

## Revision 1

### Changes

- `internal/tunnel/server.go` - moved the ready/publication boundary into `publishAuthenticated`: the bounded `server.authenticated` response is written successfully and transport/control authentication deadlines are cleared before `Manager.Register` can publish the connection or evict an older healthy one. Pre-auth slot release and source-limit success now occur only after publication.
- `internal/tunnel/manager_test.go` - added a failed-authenticated-write regression proving the candidate is never published, the pre-auth success callback is not invoked, and the existing healthy connection remains installed and open.
- `internal/tunnel/lifecycle_test.go` - added deterministic real WebSocket/yamux Gateway coverage for missed-heartbeat timeout without peer cancellation; authenticated newest-wins and exact-instance cleanup; global/per-source pre-auth limits and slot release after disconnect/authentication; rejected proof causing no Store, pairing, or online publication; and accepted authentication flowing through the real SQLite pairing service, browser claim API, owner lookup, and `online: true` response.
- `internal/tunnel/client_test.go` - added real reconnect cycles and prompt context cancellation while an actual failed dial is in a one-hour injected backoff. The revision-1 reconnect sequence asserted 1s/2s delays but did not first advance backoff before its stable session, so its reset oracle was incomplete; revision 2 corrects it below.
- `internal/wsstream/conn_test.go` - added repeated/concurrent idempotent Close and a blocked write interrupted by `SetWriteDeadline`.
- `cmd/aisummoner-client/main.go`, `main_test.go` - extracted the unchanged root policy into a pure helper and proved root is allowed only when both development flags are explicit; ordinary users remain allowed.

### Verification

- Environment check: `free -h` and `ssh ASD-Host 'free -h; df -h /tmp; go version'`
  Result: **PASS**; local had about 15 GiB available memory and 9.3 GiB free swap. ASD-Host had about 5.2 GiB available memory, 2.0 GiB free swap, 3.8 GiB available under `/tmp`, and Go 1.24.4.
- Remote formatting in isolated `/tmp/aisummoner-task002-r1.BCYD8s`: `gofmt -w` on all revision files, followed by a `gofmt -l` empty-output assertion.
  Result: **PASS**.
- Required focused tests plus root guard: `GOMAXPROCS=2 go test -count=1 -p 2 ./internal/protocol ./internal/identity ./internal/wsstream ./internal/tunnel ./internal/device ./cmd/aisummoner-client`
  Result: **PASS**; protocol, identity, wsstream, tunnel, and client command passed; device compiled with `[no test files]`.
- Required build: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Repeat stability: `GOMAXPROCS=2 go test -count=5 -p 1 ./internal/wsstream ./internal/tunnel ./cmd/aisummoner-client`
  Result: **PASS** for all five iterations.
- Race checks, serialized for memory safety: `GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/tunnel`, followed by `GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/wsstream ./cmd/aisummoner-client`
  Result: **PASS** for all packages.
- Post-verification memory check on ASD-Host.
  Result: **PASS**; about 5.2 GiB remained available and swap usage remained negligible.

### Remaining Issues

- The revision-1 reviewer found that the stable-online reset test did not distinguish the production reset branch from an implementation without it. Revision 2 addresses that test-oracle gap below. Production TLS and three-host process validation remain deliberately assigned to later tasks.

## Revision 2

### Changes

- `internal/tunnel/client_test.go` - made the stable-online reset oracle deterministic and behaviorally discriminating: two short authenticated sessions advance the reconnect bases to 1s then 2s, a third session remains online beyond `StableOnline`, and the next base must reset to 1s. Without the production reset assignment, the observed third base is 4s and the test fails.
- No production code changed in this revision.

### Verification

- Environment gate: `free -h && ssh ASD-Host 'free -h; df -h /tmp; go version'`
  Result: **PASS**; local reported about 14 GiB available memory and 9.3 GiB free swap. ASD-Host reported about 5.2 GiB available memory, 2.0 GiB free swap, 3.8 GiB available under `/tmp`, and Go 1.24.4. The focused Go tests ran serially with `GOMAXPROCS=2` and `-p 1`.
- Focused reset regression in isolated `/tmp/aisummoner-task002-r2.rPArOg`: `GOMAXPROCS=2 go test -count=1 -p 1 ./internal/tunnel -run '^TestClientReconnectsAndResetsBackoffAfterStableOnline$'`
  Result: **PASS** in 0.327s.
- Repeat stability: `GOMAXPROCS=2 go test -count=10 -p 1 ./internal/tunnel -run '^TestClientReconnectsAndResetsBackoffAfterStableOnline$'`
  Result: **PASS** for all ten iterations in 3.191s.
- Mutation proof in the isolated ASD-Host staging tree: temporarily delete only `backoffIndex = 0`, run the focused test once, then restore `client.go`.
  Result: **EXPECTED FAILURE**; the assertion observed `[1s 2s 4s]` instead of `[1s 2s 1s]`, proving the revised test detects removal of the stable-online reset. An earlier exploratory mutation that removed the whole conditional failed to compile because `onlineFor` became unused; it was discarded and was not treated as behavioral evidence.
- Restored-source formatting and package check: `test -z "$(gofmt -l internal/tunnel/client_test.go)" && GOMAXPROCS=2 go test -count=1 -p 1 ./internal/tunnel`
  Result: **PASS**; the package passed in 0.819s after the mutation copy was restored. Post-test ASD-Host memory remained about 5.2 GiB available with negligible swap use.

### Remaining Issues

- None from the revision-1 review. Production TLS and three-host process validation remain assigned to later tasks.
