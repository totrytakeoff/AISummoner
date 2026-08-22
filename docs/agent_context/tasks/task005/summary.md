---
task_id: task005
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 1
review_required: true
---

# Task 005 Summary

## Files Changed

- `internal/terminal/handler.go` - implements the standalone authenticated, owner-scoped Terminal handler, predictable WebSocket-handshake validation, captured raw-connection force close, atomic per-user/device admission, and joined lifecycle registry.
- `internal/terminal/session.go` - implements the strict binary/resize bridge and the single-coordinator close sequence that joins the close worker and all three data-plane workers.
- `internal/terminal/handler_test.go` - covers ordered preflight, generation/admission races, lifecycle joins, completion-before-removal ordering, envelopes, partial writes, and strict resize validation.
- `internal/terminal/websocket_test.go` - covers real coder/websocket data flow, real HTTP handshake failures, ResponseWriter unwrapping, raw silent peers, termination paths, limiter behavior, slow-writer backpressure, panic recovery, and log redaction.

## Behavior Changed

- `GET /api/v1/devices/{device_id}/terminal` keeps the frozen application-gate order: route/method, exactly one exact Origin, authentication, owner lookup, online state, WebSocket-handshake validation, then atomic admission.
- Before admission and `websocket.Accept`, the handler now validates HTTP/1.1+, `Connection: Upgrade`, `Upgrade: websocket`, version 13, exactly one valid base64 16-byte key, exact same Origin/Host, and Hijacker availability through `Unwrap`. Predictable failures use the standard non-secret JSON/request-ID envelope and cannot consume a Terminal slot or open a PTY.
- The production accept wrapper captures the actual `net.Conn` returned by Hijack. Cleanup starts exactly one static coder/websocket close handshake; after the configured short grace it cancels workers, closes the PTY, directly closes that raw socket when the handshake remains blocked, and joins the close worker plus reader/writer/PTY-wait workers before capacity release.
- `release` closes `session.done` while holding the lifecycle mutex before deleting the session/user slot. A caller that observes the session absent therefore also observes handler completion; `CancelDevice` and `Close` cannot miss the join window.
- Device invalidation generations, four-Terminals-per-user admission, binary-only Terminal bytes, strict bounded resize, fixed-buffer direct output/backpressure, short/zero-progress input handling, static close categories, and secret-free logging remain intact.

## Revision 1

### Review Fixes

- Replaced the ineffective concurrent public `Conn.CloseNow` fallback with a production `ForceClose` that closes the exact captured raw network connection.
- Added two real manual-TCP WebSocket tests whose peers stop after the `101` response and never read or answer a close frame. Opener failure and `CancelDevice` both complete near an injected 80 ms grace (and below one second), release the slot/session, and join PTY workers where present.
- Established the requested `finish -> registry removal` happens-before edge under `Handler.mu`, with deterministic `CancelDevice` and `Close` barrier tests rather than timing sleeps.
- Added real HTTP/1.0, missing/malformed Upgrade, missing/invalid version/key, duplicate key, Host mismatch, non-Hijacker, and Unwrap-preserved-upgrade regressions. Failure responses assert JSON content type, matching response/payload request IDs, no reflected sentinel, zero PTY opens, and zero admission residue.

### Verification

- Environment: fresh isolated ASD-Host staging at `/tmp/aisummoner-task005-r1.0QBarq`, no concurrent Go/compiler/linker process, `GOMAXPROCS=2`; `MemAvailable` was 5,466,908-5,490,172 KiB during final verification and `SwapFree` was 2,062,420 KiB.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 ./internal/terminal`
  Result: **PASS** (`ok github.com/aisummoner/aisummoner/internal/terminal 0.257s`; external wall time 1.429s).
- Command: `GOMAXPROCS=2 go test -count=10 -p 1 ./internal/terminal`
  Result: **PASS** (`ok github.com/aisummoner/aisummoner/internal/terminal 2.524s`; external wall time 3.648s).
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/terminal`
  Result: **PASS** (`ok github.com/aisummoner/aisummoner/internal/terminal 1.308s`; external wall time 3.191s).
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  Result: **PASS** (no output; external wall time 1.406s).
- Two earlier pre-test attempts were stopped and corrected transparently: the first staging copy omitted the repository `migrations/` package and failed dependency setup in 0.188s; the second reached test compilation and exposed a missing `encoding/json` test import in 0.646s. Neither executed a test body. The corrected unchanged command then passed as recorded above.
- Final local/staging SHA-256 matched after gofmt: `handler.go` `ca6b76691ed0273e5bf37fac3bf533a8a9e76a5601816b4e97580ffd21252304`; `session.go` `92320bdf5a5b1e4b6cbd96fec24cee04f08e321c91ca330689012928672a5070`; `handler_test.go` `f6cad491eac8ec632c434f2faf57e4da69932756d0eb74b369c1227232a72e93`; `websocket_test.go` `a724289341c269f4cf958370dfd691c779a9a2183593f2c63a2cb5248fbd7c8a`.
- The isolated staging directory was removed. No service, process, binary, or secret was left on ASD-Host.

## Deviations From Plan

- None. Task008 still owns HTTP composition and the closure adapting `sshclient.Dialer.OpenPTY` to `terminal.OpenPTYFunc`.

## Known Issues / Follow-Up

- Integration wrappers must either preserve `http.Hijacker` directly or implement `Unwrap() http.ResponseWriter`; Task005 validates this before admission. Task008 must call joined `CancelDevice(deviceID)` after a successful unpair commit and `Close()` during bounded Server shutdown.
