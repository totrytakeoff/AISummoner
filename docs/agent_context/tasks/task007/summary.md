---
task_id: task007
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 0
review_required: true
---

# Task 007 Summary

## Files Changed

- `internal/opencode/adapter.go` - OpenCode HTTP Adapter, strict DTO/status/auth/redirect/body handling, Turn lifecycle cleanup and health diagnostics.
- `internal/opencode/sse.go` - bounded SSE parser with prompt/assistant identity correlation, per-text family latching and stable provider error mapping.
- `internal/opencode/workspace.go` - private opaque per-Session workspace creation and verification-only reuse.
- `internal/opencode/*_test.go` - deterministic fake HTTP/SSE, workspace, lifecycle, cancellation, fatal-race and boundary tests.
- `internal/opencodebridge/bridge.go` - standalone loopback callback, HMAC proof, active Session capability registry, serialization, deadlines and joined shutdown.
- `internal/opencodebridge/bridge_test.go` - HMAC/security, cross-Session isolation, concurrency, deadline, response-bound and redaction tests.
- `opencode.json`, `.opencode/tools/remote_exec.ts`, `internal/opencode/assets/**` - canonical deny-by-default policy and embedded reviewed remote-only tool.
- `.env.example` - documents the existing bridge URL variable and same-loopback topology requirement.

## Behavior Changed

- The Adapter uses loopback-only Basic-Authenticated OpenCode HTTP, rejects redirects, normalizes the exact optional `opencode/` model prefix, creates/reuses the persisted external `ses_*`, subscribes before prompt dispatch and requires exactly 204 before waiting for a correlated terminal event.
- Every prompt carries a generated `msg_*` ID. SSE output is accepted only for an assistant whose immutable `parentID` equals that prompt ID. A user echo can classify a current prompt error but cannot authorize idle success. Stale/foreign events, tool events and unknown events cannot select a product Session or duplicate Task006 tool state.
- `message.part.delta`/growing `message.part.updated` and `session.next.text.delta` are bounded and de-duplicated per text identity. OpenCode 1.18.11 exposes optional `textID`: before a missing ID, text families latch independently per part; the first missing-ID compatibility event activates a conservative whole-message fallback to the first actually projected family (or compatibility if none), so later presence changes remain compatible without cross-family duplication. Empty/equal/stale full snapshots do not select a family.
- Turn cleanup first makes the bridge mapping inactive and joins every executing/queued callback, then cancels/closes and joins the SSE parser before returning, and finally best-effort aborts failed/canceled provider work. A callback fatal concurrent with provider idle retains the original error.
- Workspaces are stable SHA-256/base32 paths below a 0700 root and contain only 0600 reviewed config/tool bytes. Existing workspaces are verified without repair; missing, extra, changed or symlinked entries fail closed.
- The bridge accepts only loopback POST JSON authenticated by the frozen Session/timestamp HMAC, looks up only an active external Session's injected invoker, serializes its whole call, caps body/response sizes and callback duration, and cancels/joins on deactivation. It never accepts device/user selectors or runs local commands.
- The TypeScript tool validates the exact loopback callback URL, rejects redirects, forwards `context.abort`, signs the external Session proof and returns stdout/stderr/exit/truncation/denial/failure evidence without exposing its secret.

## Verification

- Final isolated ASD-Host staging was formatted with `gofmt`, copied back, and all eight Task007 Go source/test SHA-256 values matched between the workspace and staging. Root/embedded `opencode.json` and `remote_exec.ts` pairs matched byte-for-byte.
- Command: `GOMAXPROCS=2 go test -count=1 -p 1 ./internal/opencode ./internal/opencodebridge`.
  Result: PASS (`opencode` 0.048s; `opencodebridge` 1.248s).
- Command: the optional-`textID` fallback, empty-full-snapshot, Adapter late/bridge fatal, bridge delivery and real-TCP deadline tests with `-count=10 -p 1`.
  Result: PASS (`opencode` 0.042s; `opencodebridge` 11.792s).
- Command: `GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/opencode ./internal/opencodebridge`.
  Result: PASS (`opencode` 1.259s; `opencodebridge` 2.743s).
- Command: `GOMAXPROCS=2 go vet -p 1 ./internal/opencode ./internal/opencodebridge`.
  Result: PASS.
- Command: `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-client ./cmd/aisummoner-server`.
  Result: PASS.
- Two pre-test orchestration attempts did not reach package compilation: Go 1.24 interpreted the mistyped `-p1` as the current package, and the next SSH connection timed out before entering the remote command. Both were fail-stopped, resource/process checked, corrected, and followed by the successful sequence above.
- Final ASD-Host resource check after cleanup: about 5.48 GiB MemAvailable and 2.06 GiB SwapFree, with no Go job left. Both Task007 isolated staging directories were removed.

## Deviations From Plan

- No live inference/free-model call was used for deterministic acceptance. The implementation follows the documented/probed OpenCode 1.18.11 HTTP/SSE DTO, including prompt `messageID`, assistant `parentID` and compatibility `textID`; production availability is not claimed.

## Known Issues / Follow-Up

- Task008 must instantiate and route these constructors, supply a strong per-process bridge secret, mount the callback only on a separate real loopback HTTP server (where ResponseController deadlines are supported), and manage the OpenCode sidecar. Separate containers do not share `127.0.0.1`; use a shared network namespace or equivalent same-host loopback topology.
- Starting/managing OpenCode and selecting external credentials/model quota remain deliberately outside Task007.
