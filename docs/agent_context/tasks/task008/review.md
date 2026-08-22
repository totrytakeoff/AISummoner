---
task_id: task008
type: review
from: reviewer
to: orchestrator
revision: 0
status: approved
decision: APPROVED
next_action: next_task
---

# Task 008 Review

## Decision

APPROVED

## Findings

No blocking source, lifecycle, composition, route, configuration, static-Web,
redirect-hardening, or deployment-contract issue remains in the frozen
revision-2 scope.

The production composition constructs exactly one `devicegate.Gate` and
injects it into both Tunnel publication and Device unpair. The durable unpair
transaction consumes active codes, revokes all old Device Sessions,
terminalizes active tools and clears ownership atomically. After commit,
Device Service installs Agent tombstones and logically detaches the exact
Tunnel while still holding that gate, then releases it before joined
Tunnel/Terminal/Agent cleanup. Manager replacement, retirement and shutdown
retain ownership of network cleanup without delaying a new connection's
publication. Gateway publishes only after authenticated and, when unowned,
fresh-pairing frames have been written; its admission/Close boundary joins
pre-authenticated and authenticated hijacked handlers and the extra-stream
watcher.

Agent owner-facing SQL consistently requires the product user, a non-revoked
Session and current Device ownership. The in-memory tombstone/cancellation
boundary, second pre-executor revocation check, joined Turn completion and SSE
subscriber closure prevent an old idle, pending, running or `full_access`
Session from recovering authority after same-owner re-pair. Deterministic
tests exercise both Gateway/unpair orders, blocked old-connection cleanup,
same-owner re-pair before releasing an old Turn, Store rollback, subscription
admission, cleanup timeout/retry and Gateway/Manager close admission. These
oracles would fail on the unsafe publication, detach, tombstone or Add/Wait
orders they are intended to exclude.

The process root wires the strict SSH client into Terminal and Agent, selects
only Fake or OpenCode, pre-binds the private loopback Bridge before the
OpenCode health gate, never mounts that Bridge publicly, and transfers sole
resource ownership to Runtime only after construction succeeds. Dispatcher
selection preserves the original `ResponseWriter`; exact API/Tunnel/Terminal/
Agent paths cannot fall through to the SPA. Runtime closes admission, joins
Agent, Terminal and Tunnel, then Bridge/public HTTP, and closes SQLite last;
when a trusted lifecycle owner misses the total deadline it deliberately
leaves SQLite open rather than closing it underneath live work.

Configuration fails closed for unknown adapters, public HTTP with a
non-literal-loopback listener, non-loopback OpenCode/Bridge endpoints, missing
OpenCode credentials and undersized secrets. Fake mode returns before reading
OpenCode-only configuration. Remote plaintext is restricted to literal
loopback and Tunnel WebSocket handshakes never follow redirects. The static
handler separates API/internal/health paths, limits SPA fallback to
extensionless browser routes and applies immutable caching only to validated
hashed assets. The Docker source replaces the tracked clean-checkout
placeholder with the lockfile-built Web output before Go compilation; Compose
publishes only Caddy, keeps OpenCode in the Server network namespace, uses the
pinned `opencode-ai 1.18.11` image and separates SQLite from the shared
workspace. The Remote systemd unit keeps pairing stdout out of journald and
documents the private-file inspection and secure truncate workflow.

The production Server image was not produced: the authorized retry failed at
`go mod download` after 120.3 seconds while connecting to
`proxy.golang.org` for `github.com/coder/websocket@v1.8.14`. This is a
non-blocking environmental residual for Task008 because the plan explicitly
permits an exact failed Docker result when the environment cannot complete the
gate, the same frozen source passed direct Go/Web verification, and the
failure occurred before source compilation rather than at a Dockerfile or
application assertion. It is not evidence of a successful production image.
Task010 must retry the unchanged Dockerfile from a network-capable host, or use
the already verified binary path for controlled deployment, before accepting
runtime deployment. Dynamic image/static-asset smoke, systemd pairing-file
mode and the three-host TLS/WSS acceptance likewise remain Task010 gates.

## Required Fixes

None.

## Reviewer Verification

- Independently read the Codgent reviewer workflow, `AGENTS.md`, all four
  baseline documents, ADR-0001/0002, Task008 revision-2 plan, frozen summary,
  lifecycle handoff, Task003 revision-4 approval, and the final relevant
  lifecycle/composition/config/static/deploy/operational source and tests.
  Result: **PASS** against every Task008 acceptance boundary above.
- Independently traced the real composition fixtures and mutation-sensitive
  lifecycle tests rather than relying on the summary's claims. Result:
  **PASS** for route capabilities, full Fake Remote/SSH/Terminal/Agent flow,
  unpair invalidation and fresh pairing, private OpenCode startup/Bridge
  routing, startup unwind and joined shutdown.
- Author final evidence on the hash-matched frozen tree: `go test ./...` passed
  all 24 targets; `go test -race ./internal/...` passed all 21 internal
  packages including the Task003-r4 SSH composition; `go vet ./...` and both
  command builds passed. Clean `npm ci`, all 8 Web test files/22 tests and the
  Vite production build passed; Fake/OpenCode Compose validation was
  secret-silent; the pinned OpenCode image built and returned exactly
  `1.18.11`. Result: **RECORDED PASS**.
- Independent heavy rerun: not performed. The final Go/race/Web/Compose
  evidence is fresh and resource-gated, and source inspection was sufficient
  to classify the separately recorded Server-image network timeout.

## Next Action

Task008 is approved. Proceed to Task009/Task010 as planned; do not treat this
approval as production deployment acceptance until the residual Docker and
three-host runtime gates above pass.
