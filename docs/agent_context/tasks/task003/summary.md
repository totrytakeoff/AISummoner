---
task_id: task003
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 4
review_required: true
---

# Task 003 Summary

## Files Changed

- `internal/sshserver/server.go`, `server_test.go` - added an Embedded SSHD served directly on a Tunnel stream, exact Ed25519 client-key authentication, strict session request/channel allowlists, non-PTY exec, PTY shell/resize, exit status, signal handling, cwd validation and Linux process cleanup.
- `internal/sshclient/client.go`, `client_test.go` - added strict Device host-key verification, atomic Tunnel stream/signer dialing, separate bounded exec stdout/stderr/status, Store key adapter and the narrow PTY handle used by later tasks.
- `internal/tunnel/manager.go`, `manager_test.go` - replaced independent stream/signer access with atomic `OpenSSH`, bounded concurrent setup, typed stream headers, cancellation and a newest-wins same-Connection regression.
- `internal/identity/identity.go`, `identity_test.go` - added `SSHSigner()` backed by the existing Device Identity without exporting private bytes, plus exact Ed25519-key coverage.
- `cmd/aisummoner-client/main.go`, `main_test.go` - wired authenticated `kind:ssh` streams to Embedded SSHD using the Device host signer and connection-scoped client public key; added a narrow construction test.
- `go.mod`, `go.sum` - added the frozen `github.com/creack/pty v1.1.24` dependency.

## Behavior Changed

- Remote now serves SSH directly over Server-opened yamux streams and never listens on TCP. It accepts only user `aisummoner`, the exact current connection Ed25519 key and `session` channels; global, forwarding, agent, X11 and unknown channels fail closed.
- Each session accepts at most one launch. Exec uses the Remote user's absolute executable `$SHELL` or `/bin/sh`, arguments `-lc <command>`, no PTY, and separate stdout/stderr. Only one bounded absolute `AISUMMONER_CWD` is allowed and is `os.Stat`-validated immediately before launch.
- PTY requires a valid `pty-req`, starts `xterm-256color` interactive shell semantics, preserves `(cols, rows)` ordering through SSH and accepts bounded `window-change` only while running.
- The Server obtains stream and signer atomically from the same live Tunnel Connection. SSH verifies the exact registered raw Ed25519 Device key; no insecure/TOFU fallback exists. Setup deadlines are cleared only after the handshake.
- Exec capture has a caller-selected hard maximum and a shared combined budget across stdout/stderr. Both streams continue draining after truncation. Non-zero exits remain a result, while authentication, protocol and context failures remain errors.
- Context, stream, session and PTY closure are idempotent and affect only that SSH stream. A process group ID is captured immediately after start. PTY cleanup additionally enumerates Linux `/proc/<pid>/stat` by the PTY session ID because interactive shells may create separate job process groups; only processes in that session are signaled. This is intentionally Linux-only, matching MVP-0.
- `exec.Cmd.WaitDelay` bounds a child that exits while inherited I/O descriptors remain open. If the leader exited successfully and only `exec.ErrWaitDelay` occurred, exit status remains zero; final cleanup still kills session descendants. A server-owned stdin pipe also prevents exec completion from depending forever on client EOF.
- No command, cwd, environment value, Terminal input, stdout or stderr is logged.

## Verification

- Resource gate before ASD-Host work: `free -h && ssh ASD-Host 'free -h; pgrep -af "go (test|build)|/compile|/link" || true; df -h /tmp'`
  Result: **PASS**; local had about 13 GiB available memory and 9.3 GiB free swap. ASD-Host had about 5.2 GiB available, 2.0 GiB free swap, no competing Go build/test, and 3.8 GiB available under `/tmp`. Tests were serialized with `GOMAXPROCS=2` and `-p 1` or the required build's `-p 2`.
- Focused required packages plus touched wiring: `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 180s ./internal/sshserver ./internal/sshclient ./internal/tunnel ./internal/identity ./cmd/aisummoner-client`
  Result: **PASS**; all five packages passed. Coverage includes exact/wrong host, exact/wrong/non-Ed25519 client key, bad user, Store key clone/malformed/error, global/channel/request allowlists, malformed/oversized payloads, exec stdout/stderr/non-zero status, exact 64 KiB output, combined truncation/drain, cwd, deadline/cancel, stdin without EOF, PTY initial size/resize/close, and process-group/session descendant cleanup.
- Repeat lifecycle stability: `GOMAXPROCS=2 go test -count=5 -p 1 -timeout 300s ./internal/sshserver ./internal/sshclient ./internal/tunnel`
  Result: **PASS**; SSH server passed in 0.006s, SSH client in 6.007s and Tunnel in 3.946s for all five iterations.
- Dependency and static checks: `GOMAXPROCS=2 go mod tidy` followed by zero-diff assertions; `test -z "$(gofmt -l ...)"`; `GOMAXPROCS=2 go vet ./internal/sshserver ./internal/sshclient ./internal/tunnel ./internal/identity ./cmd/aisummoner-client`
  Result: **PASS**; module files were already tidy, formatting output was empty and vet produced no output.
- Required builds: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Post-verification resource check on ASD-Host.
  Result: **PASS**; approximately 5.2 GiB remained available and swap usage remained negligible.

## Deviations From Plan

- Local still has no Go toolchain, so Go formatting, tests, vet and builds ran in isolated ASD-Host `/tmp/aisummoner-task003.Uh3AOm` with constrained concurrency.
- A supplemental authenticated WebSocket/yamux/SSH test was attempted and removed before delivery because its artificial warmup stream could race SSH version bytes across successive streams; repeat execution failed with `SSH handshake: EOF` / `overflow reading version string`. This was not reported as passing. The delivered deterministic coverage keeps real SSH over full-duplex Unix sockets plus the actual yamux `OpenSSH` header/signer boundary; three-host process coverage remains task010.
- Race testing was not run to avoid overlapping Task006 resource use and because the orchestrator requested only later/serialized race execution.

## Known Issues / Follow-Up

- Task005 and Task006 should inject narrow closure adapters around `Dialer.OpenPTY` and `Dialer.Exec`; their existing interfaces use task-owned result/handle types, so the concrete methods intentionally do not satisfy those interfaces directly.
- Terminal HTTP/WebSocket ownership checks, Agent approval integration, production TLS and three-host process validation remain assigned to tasks 005/006/010.

## Revision 1

### Changes

- `internal/tunnel/client.go`, `client_test.go` - each authenticated yamux run now has a child cancellation scope. The accept loop is the sole owner of handler `WaitGroup.Add`; it stops accepting before waiting for every SSH stream handler. `runAuthenticated` closes the run/session/control stream and joins the accept loop, all SSH handlers, heartbeat writer and control reader before returning. Barrier regressions cover both root cancellation and transport disconnect without an `Add`/`Wait` race.
- `internal/tunnel/client.go`, `client_test.go` - fixed the removed composition test's framing race. A temporary `protocol.Codec` used a buffered reader that could prefetch SSH version bytes while reading the typed stream header and then discard them. Remote dispatch now reads exactly the four-byte length and one bounded header frame before strict decoding, leaving every following SSH byte untouched; a focused byte-preservation regression covers this boundary.
- `internal/sshserver/server.go`, `server_test.go` - `process.done` now means the command has exited and been reaped, the SSH channel/PTY/stdin have been closed in an order that unblocks I/O, and every tracked exec/PTY copy pump has joined. `Handler.Serve` also joins session handlers and the global-request drain before returning.
- `internal/sshserver/server.go`, `server_test.go` - process cleanup now uses Linux identity-stable signaling. The leader pidfd is opened immediately after `Start`, its PID/process-group/session/start-time identity is verified and retained, and `waitid(P_PIDFD, WEXITED|WNOWAIT)` keeps the exited leader unreaped while final descendant cleanup runs. `/proc` candidates are opened as pidfds first, their pidfd identity plus post-open `stat` SID/PGID/start-time are rechecked, and signals are sent only with `pidfd_send_signal`; no enumerated positive PID is passed to `kill(2)`.
- `internal/sshserver/server.go`, `server_test.go` - final cleanup repeats pidfd-only scope scans while the leader remains a zombie, covering descendants forked after an earlier snapshot. It requires two consecutive empty scans, has a two-second bound, and converts failure to quiesce into SSH exit status 255 rather than silently publishing the original command status. Deterministic injected regressions simulate numeric PID reuse between inspection and delivery and a late descendant appearing after the first signal. The real composition regression also proves an unrelated same-UID sentinel survives.
- `internal/sshclient/tunnel_integration_test.go` - added stable, no-warmup `TestTunnelSSHEndToEnd`: authenticated development WS/yamux Client/Gateway, `Manager.OpenSSH`, typed Remote dispatch, real Embedded SSHD and strict `sshclient.Dialer`. It proves exact Device/host and connection signer alignment, separate stdout/stderr/non-zero exit, PTY initial dimensions and resize, single-Exec context cancellation without closing the Device Tunnel, whole-Tunnel cancellation, joined handler completion, and child reap before `Client.Run` returns. Production TLS/WSS remains Task010.
- `internal/tunnel/manager_test.go` - added explicit `OpenSSH` setup-slot cancellation/ownership and repeated successful release coverage.
- `go.mod`, `go.sum` - made the already locked `golang.org/x/sys v0.34.0` dependency direct for Linux pidfd and `waitid`; `go mod tidy` did not upgrade it.

The Embedded SSHD is Linux-only as frozen by MVP-0 and now requires kernel support for `pidfd_open`, `pidfd_send_signal`, and `waitid(P_PIDFD, ... WNOWAIT)`, plus `/proc` for scope enumeration. Process launch/cleanup fails closed when the required identity boundary cannot be established. Exec descendants are scoped by the isolated process group; PTY descendants are scoped by the isolated session so jobs that create a different process group are still removed.

### Verification

- Resource gate on ASD-Host before Go work: approximately 5.2 GiB memory available, 2.0 GiB swap free, no competing Go test/build, and 3.8 GiB free in `/tmp`. All commands were serialized with `GOMAXPROCS=2` and `-p 1` except the approved two-command build with `-p 2`.
- Command: `gofmt -w internal/tunnel/client.go internal/tunnel/client_test.go internal/tunnel/manager_test.go internal/sshserver/server.go internal/sshserver/server_test.go internal/sshclient/tunnel_integration_test.go && GOMAXPROCS=2 go mod tidy && GOMAXPROCS=2 go test -count=1 -p 1 -timeout 240s ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** in isolated ASD-Host staging; packages passed in 0.008s, 1.004s and 0.480s respectively. Tidy retained `x/sys v0.34.0` and only changed its classification from indirect to direct.
- Command: `GOMAXPROCS=2 go test -count=10 -p 1 -timeout 360s ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** in 15 seconds; packages passed in 0.042s, 8.965s and 4.732s. This repeated the complete production revision and initial no-warmup composition test before the final test-only addition of the explicit per-Exec context-cancel segment.
- Command: `GOMAXPROCS=2 go test -count=5 -p 1 -timeout 180s ./internal/sshclient -run '^TestTunnelSSHEndToEnd$'`
  Result: **PASS** before the final test-only context-cancel segment (0.583s) and **PASS again on the final tree** after that segment (0.695s). There is no warmup stream in either run.
- Final-tree command after the test-only context-cancel addition: `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 240s ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS**; packages passed in 0.009s, 0.986s and 0.506s.
- Command: `GOMAXPROCS=2 go vet ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** with no output.
- Final-tree command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output (one-second shell timing).
- Integrity check: SHA-256 was compared for every revised/tested production/test file plus `go.mod`/`go.sum`; the local files exactly matched the formatted, tested and built ASD-Host copies. The isolated staging directory `/tmp/aisummoner-task003-r1.76asc8` was then removed. Post-run ASD-Host resources remained approximately 5.2 GiB available memory, 2.0 GiB free swap and 3.8 GiB free `/tmp`, with no Go job left running.
- Two setup-only attempts failed before any validation ran and are not counted as test evidence: the first transfer used unavailable remote `rsync`; a timing wrapper used unavailable `/usr/bin/time`. Transfer was switched to a narrow `scp` staging copy and shell timestamps were used.

### Deviations From Plan

- Local still has no Go toolchain. Formatting, module normalization, tests, vet and builds ran in the approved isolated ASD-Host directory with constrained concurrency, then exact hashes were checked before staging cleanup.
- Race and repository-wide tests were not run; the orchestrator explicitly limited this revision to focused repeated tests, vet and the two command builds to avoid competing with other task work.

### Remaining Issues

- None within Task003 revision 1. Browser Terminal ownership/gateway, Agent approval integration, deployment and three-host process acceptance remain in their assigned later tasks.

## Revision 2

### Changes

- `internal/sshserver/server.go` - added one per-process lifecycle mutex and explicit `active`/`finalizing`/`finished` states. An SSH `signal` request now holds lifecycle ownership from its pre-anchor state check through leader identity verification, pidfd delivery and the complete descendant scope scan. `beginFinalization` occurs only after `waitid(P_PIDFD, WEXITED|WNOWAIT)` observes leader exit; it waits for any in-flight signal, then makes every late signal fail before reading the anchor or numeric PGID/SID.
- `internal/sshserver/server.go` - final cleanup no longer re-enters public `signal`/`terminate`. The exclusive finalizer uses the still-unreaped leader pidfd for its own identity-verified `SIGKILL` and bounded repeat scan, then calls `exec.Cmd.Wait`. SSH channel/PTY/stdin closure unblocks the copy pumps; only after all pumps join does the finalizer close the pidfd anchor, publish `finished` and close `process.done`. No lifecycle lock is held while waiting for child exit, doing SSH channel I/O or joining pumps.
- `internal/sshserver/server.go` - `processAnchor` now protects its descriptor, operations and closed state with an RW lock. A verified operation leases the descriptor continuously from PID/start-time verification through pidfd send and scope enumeration; `Close` cannot recycle it in that interval, and `fd = -1` is no longer a data race.
- `internal/sshserver/server_test.go` - added deterministic no-warmup barriers at verified pidfd delivery and descendant scope enumeration. They prove finalization/anchor close waits for an in-flight signal, leader reap cannot cross an in-flight scope scan, and a late signal is rejected without a stale identity lookup. The focused race detector covers the same paths.
- Corrected the revision-1 composition description from WSS to development WS. `httptest.NewServer` covers the authenticated Gateway/WS/yamux/SSH composition; production TLS/WSS is still Task010.

### Verification

- Initial ASD-Host resource gate: approximately 5.2 GiB memory available, 2.0 GiB swap free, no `go`, `compile` or `link` process, and 3.8 GiB free under `/tmp`. All work used isolated staging and `GOMAXPROCS=2`; tests used `-p 1` and the approved build used `-p 2`.
- Command: `gofmt -w internal/sshserver/server.go internal/sshserver/server_test.go && GOMAXPROCS=2 go test -count=1 -p 1 -timeout 180s ./internal/sshserver`
  Result: **PASS**; package passed in 0.036s.
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 240s ./internal/sshserver`
  Result: **PASS**; package passed in 1.079s with no race report.
- Command: `GOMAXPROCS=2 go test -count=20 -p 1 -timeout 300s ./internal/sshserver`
  Result: **PASS**; all twenty iterations passed in 0.614s.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 300s ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS**; packages passed in 0.034s, 1.025s and 0.467s respectively.
- Command: `GOMAXPROCS=2 go test -count=5 -p 1 -timeout 240s ./internal/sshclient -run '^TestTunnelSSHEndToEnd$'`
  Result: **PASS**; all five no-warmup development WS/yamux/SSH composition iterations passed in 0.646s.
- Command: `GOMAXPROCS=2 go vet ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** with no output.
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Integrity and cleanup: local `server.go`, `server_test.go`, `go.mod` and `go.sum` hashes exactly matched both formatted/tested ASD-Host staging copies. Both staging directories and the local transfer archive were removed. Final ASD-Host state remained about 5.2 GiB available memory, 2.0 GiB free swap and 3.8 GiB free `/tmp`, with no Go tool process left running.

### Deviations From Plan

- Local still has no Go toolchain. Formatting, focused/repeated/race tests, vet and builds ran in orchestrator-approved isolated ASD-Host staging under the required resource gate.
- Repository-wide tests were not run; revision 2 was intentionally limited to the review's single process lifecycle blocker plus proof that the previously approved Task003 packages and no-warmup composition remain green.

### Remaining Issues

- None within Task003 revision 2. Task005/006 retain the documented narrow closure-adapter requirement, and production TLS/WSS plus three-host process acceptance remain Task010.

## Revision 3

### Changes

- `internal/sshserver/server.go` - added a PTY-specific lifecycle mutex and explicit `active`/`finalizing`/`closed` states. A `window-change` request now retains PTY lifecycle ownership from its state check through the complete `pty.Setsize` operation. Every close of a published PTY from context/transport termination or final process cleanup goes through the same idempotent boundary, so `os.File.Close` cannot race `os.File.Fd` inside resize.
- `internal/sshserver/server.go` - `beginFinalization` uses the sole two-lock order `terminalMu` then `lifecycleMu`. It publishes the process and terminal finalizing states only after confirming the process is still active; a failed/duplicate finalizer leaves terminal state unchanged. SSH signal delivery still uses `lifecycleMu` then the pidfd anchor, and termination releases that chain before acquiring `terminalMu`. Neither lifecycle mutex is held across child wait/reap, `/proc` repeat scans, SSH channel I/O or copy-pump joins.
- `internal/sshserver/server.go` - retained the direct PTY close only on the `startShell` construction failure path, before the `process` is published and before a resize can exist. Normal resize dimensions and SSH replies are unchanged, while resize after close/finalization fails before touching the PTY file.
- `internal/sshserver/server_test.go` - added a deterministic file-operation barrier regression. It holds an in-flight resize inside the injected PTY operation, proves the PTY lifecycle lock remains held and production termination cannot enter close or complete, then releases the resize and joins both operations in order. A second regression proves late resize performs zero file operations after finalization, close remains idempotent, and a duplicate finalizer cannot mutate terminal state.

### Verification

- Initial and repeated resource gates: local retained about 7.1 GiB available memory and 9.2 GiB free swap. ASD-Host retained about 5.1-5.2 GiB available memory, 2.0 GiB free swap and 3.7 GiB free under `/tmp`, with no competing Go test/build process. All Go work ran sequentially with `GOMAXPROCS=2`, tests used `-p 1`, and the final build used `-p 2`.
- Formatting/integrity: remote `gofmt` was applied to `internal/sshserver/server.go` and `server_test.go`, copied back, and SHA-256 compared before and after every dependency sync. Final local/ASD hashes matched: `server.go` `22d2054d...`, `server_test.go` `270fd3c9...`, `go.mod` `fefb9deb...`, and `go.sum` `33f8a283...`.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 180s ./internal/sshserver`
  Result: **PASS** after the recorded test-only compile correction; package passed in 0.035s.
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 240s ./internal/sshserver`
  Result: **PASS** in 1.083s with no race report.
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 240s ./internal/sshclient -run '^TestTunnelSSHEndToEnd$'`
  Result: **PASS** in 1.295s with no race report after the recorded staging-only dependency correction. This is the exact development WS/yamux/strict SSH composition that exposed the merged resize/close race.
- Command: `GOMAXPROCS=2 go test -count=20 -p 1 -timeout 240s ./internal/sshserver -run '^TestPTYResize'`
  Result: **PASS**; all twenty deterministic resize/finalization iterations passed in 0.005s.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 -timeout 300s ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS**; packages passed in 0.036s, 1.016s and 0.454s respectively.
- Command: `GOMAXPROCS=2 go test -count=5 -p 1 -timeout 240s ./internal/sshclient -run '^TestTunnelSSHEndToEnd$'`
  Result: **PASS**; all five composition iterations passed in 0.651s.
- Command: `GOMAXPROCS=2 go vet ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** with no output.
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Cleanup: exact staging directory `/tmp/aisummoner-task003-r3.d56gqE` was removed and verified absent. Final ASD resources remained about 5.2 GiB available memory and 2.0 GiB free swap, with no Go tool process left running.

Two intermediate commands failed and are not counted as successful verification:

- The first focused test attempt stopped at compile time with `server_test.go:210:12: process is not a type`; a new test-local variable named `process` shadowed the package type used by a later fixture. It was renamed to `runningProcess`, reformatted and the same focused command then passed. Production code was unchanged by this correction.
- The first exact composition race attempt stopped during setup because the narrow staging copy omitted the root embedded `migrations` package required by `internal/store`. After orchestrator approval, the unchanged local `migrations/` files were copied with matching hashes and the exact same composition race command passed. This was a staging dependency omission, not a product-code or test-oracle failure.

### Deviations From Plan

- Local still has no Go toolchain. Formatting, focused/repeated/race tests, vet and builds ran in the orchestrator-approved isolated ASD-Host directory under the required resource gates, followed by exact hash comparison and cleanup.
- Repository-wide race was not rerun by this bounded revision. The reopened plan required the focused SSH Server race and exact full Tunnel/SSH composition under race; both passed. The broader merged race remains owned by integration verification.

### Remaining Issues

- None within Task003 revision 3. Production TLS/WSS and three-host process acceptance remain Task010 scope.

## Revision 4

### Changes

- `internal/sshserver/server.go` - added the package-private `terminalCloseContended` test hook and a narrow `lockTerminalForClose` helper. The hook is nil in production, where the helper executes the same single blocking `terminalMu.Lock` as revision 3. When a test injects the hook, it fires only after `TryLock` has actually attempted and failed to acquire the resize-held mutex, immediately before the close path waits on that same mutex. No public API or production lifecycle semantics changed.
- `internal/sshserver/server_test.go` - replaced the reviewer-rejected pre-call `terminationStarted` signal with the real close-contender admission hook. The regression now waits for positive proof that production `closeTerminal` reached and contended on `terminalMu` while resize holds it, then proves neither the PTY file close nor termination crosses the boundary before resize is released. Removing the close-side synchronization/helper makes the admission wait fail deterministically; allowing close to cross makes the close/termination assertions fail.

### Verification

- Resource gates: local had about 7.0 GiB available memory and 9.2 GiB free swap. ASD-Host remained at about 5.2 GiB available memory, 2.0 GiB free swap and 3.7 GiB free under `/tmp`, with no competing Go test/build process before each command. All Go commands were serialized with `GOMAXPROCS=2`; tests used `-p 1`, and builds used `-p 2`.
- Formatting/integrity: remote `gofmt` was applied and copied back before tests. Final local and ASD hashes matched: `server.go` `074a0dcd...`, `server_test.go` `e728f626...`, `go.mod` `fefb9deb...`, and `go.sum` `33f8a283...`.
- Command: `GOMAXPROCS=2 go test -count=20 -p 1 -timeout 180s ./internal/sshserver -run '^TestPTYResize'`
  Result: **PASS**; all twenty deterministic resize/close oracle iterations passed in 0.005s.
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 240s ./internal/sshserver`
  Result: **PASS** in 1.079s with no race report.
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 240s ./internal/sshclient -run '^TestTunnelSSHEndToEnd$'`
  Result: **PASS** in 1.262s with no race report across the development WS/yamux/strict SSH composition.
- Command: `GOMAXPROCS=2 go vet ./internal/sshserver ./internal/tunnel ./internal/sshclient`
  Result: **PASS** with no output.
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** with no output.
- Cleanup: exact staging directory `/tmp/aisummoner-task003-r4.WH7u3Z` was removed and verified absent. Final ASD resources remained about 5.2 GiB available memory and 2.0 GiB free swap, with no Go tool process left running.

### Deviations From Plan

- Local still has no Go toolchain. Formatting, tests, vet and builds ran in the orchestrator-approved isolated ASD-Host staging directory under the required resource gates, followed by exact hash comparison and cleanup.
- None otherwise. Revision 4 contains only the requested deterministic package-private test admission seam, its oracle update and this summary.

### Remaining Issues

- None within Task003 revision 4. Production TLS/WSS and three-host process acceptance remain Task010 scope.
