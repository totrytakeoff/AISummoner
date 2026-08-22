---
task_id: task009
type: plan
status: ready_for_revision
from: planner
to: revision_coders
revision: 2
requires_review: true
---

# Task 009 Plan: Whole-Repository Integration And Security Review

## Revision 2: Trusted Reverse-Proxy Source Provenance

Revision 1 closed both unbounded-resource findings, but its direct-peer-only
source key collapses all public traffic onto Caddy's container address. The
revision 1 review therefore returned `CHANGES_REQUESTED`. This amendment
supersedes only the source-provenance portions of revision 1; the two-slot KDF
gate and both 4096-entry limiter implementations remain frozen.

### Frozen Source Boundary

- Add one small `internal/requestsource` package. Its immutable Resolver is
  constructed from an explicit list of exact literal trusted-proxy IPs; DNS
  names, CIDR ranges, zones, unspecified/multicast addresses and malformed or
  duplicate values are rejected by configuration. Empty configuration means
  direct-peer mode.
- The dedicated header is exactly `X-AISummoner-Client-IP`. If and only if the
  canonical immediate `RemoteAddr` IP is in the trusted list, Resolver requires
  exactly one header value containing one canonical literal unicast client IP
  and returns that address. Missing, repeated, comma-separated, whitespace-
  ambiguous, zoned, unspecified or multicast values fail closed before a
  limiter entry is touched.
- For every untrusted direct peer, Resolver ignores the dedicated header
  completely—even if it is malformed or repeated—and returns the canonical
  immediate peer IP. `X-Forwarded-For`, `Forwarded` and all other caller-
  supplied proxy headers are never consulted.
- Browser login, pairing claim and Tunnel admission receive the same immutable
  Resolver instance from Server composition. Package-standalone constructors
  may default nil to a direct-peer Resolver, preserving existing tests and
  embedding behavior.
- Resolve errors use fixed public errors only: Browser routes return the normal
  request-linked `400 INVALID_REQUEST`; Tunnel returns a fixed 400 without
  logging the header. No source/header/config value is logged.

### Deployment Contract

- Caddy must overwrite (not append) `X-AISummoner-Client-IP` on every upstream
  request with its directly observed `{remote_host}`. It remains the only
  public listener.
- Compose gives the private edge network, Server and Caddy configurable but
  deterministic IPv4 values with safe non-secret defaults. The exact Caddy
  address is passed as `AISUMMONER_TRUSTED_PROXY_IPS`; Server trusts no other
  edge peer. OpenCode continues to share Server's network namespace but is not
  in the trusted proxy list.
- Direct development defaults to no trusted proxy. Task010's isolated host
  Caddy/TLS topology must explicitly set the exact loopback/container peer; it
  may not enable a broad subnet.
- `.env.example`, README and deploy contract tests must describe/assert this
  topology without logging values from a real `.env`.

### Disjoint Revision Workstreams

Workstream C owns only `internal/requestsource/`, `internal/config/`,
`deploy/Caddyfile`, `deploy/compose.yaml`, `.env.example`, README proxy notes,
and `internal/app/deploy_contract_test.go`. It implements/configures the
boundary and deterministic parse/anti-spoof tests.

Workstream D owns only `internal/httpapi/`, `internal/tunnel/server.go` and
focused tests, plus `cmd/aisummoner-server/server.go` and its focused tests. It
injects one Resolver into Browser and Tunnel consumers and adds production-
topology regressions: one stable trusted proxy peer represents sources A and
B; A reaching the login/Tunnel limit must not block B; an untrusted peer cannot
change key by forging any proxy header; missing/malformed trusted headers do
not create limiter state. The root source oracle must prove the exact same
Resolver variable is passed to both consumers.

Neither coder edits `review.md`, Task009 state or approves its own changes.
After both workstreams freeze, the integration owner performs the same fresh
complete-tree focused/repeat/race/full-test/vet/build gate specified in
revision 1, updates summary to revision 2, and requests another independent
review. Task010 remains blocked until approval.

## Revision 1: Public Resource-Boundary Fixes

The revision 0 independent review returned `CHANGES_REQUESTED`. Two disjoint
revision implementers are authorized to make only the following narrow fixes;
neither implementer may edit `review.md` or approve its own work.

### Workstream A: Login KDF Admission And Bounded Failure State

Owned files are `internal/auth/service.go`, focused auth tests,
`internal/httpapi/api.go`, `internal/httpapi/handlers.go`, and focused HTTP API
tests. Changes outside those files require an explicit planner amendment.

- Put a process-local, non-blocking fixed-capacity gate immediately around
  password verification. The authoritative gate belongs to `auth.Service`,
  not only the HTTP route, and all service instances use the same process-level
  capacity. Capacity is fixed at two concurrent verifications; overload must
  return a typed sentinel without entering Argon2 and must recover as soon as a
  slot is released. Context cancellation before verification must not consume
  work or a slot.
- Preserve the existing Store lookup/session semantics and never log username,
  password, hash, token, request body or raw verification error. Map overload
  to a stable JSON `503 SERVICE_UNAVAILABLE` response; do not count overload as
  a bad credential attempt.
- Add a login failure limiter before authentication. It is keyed only by the
  directly observed `RemoteAddr` host, ignores forwarded headers, uses five
  failures per one-minute window, and returns `429 RATE_LIMITED` while blocked.
  Invalid JSON and invalid credentials count as failures; success removes the
  current key. The same bounded limiter implementation must continue serving
  pairing-claim failures.
- Every failure-state map has a hard capacity of 4096 entries and one-minute
  idle/window expiry. Cleanup is synchronous and bounded—no per-key goroutines.
  On a new key at capacity, evict the least-recently-observed entry; rotating
  sources may weaken per-source fairness but can never grow memory past the
  cap.
- Deterministic tests must use a verification barrier/hook to prove maximum
  KDF concurrency is exactly bounded, excess calls fail fast without entering
  verification, cancellation/release restores admission, HTTP overload has the
  stable envelope, five failures block and expiry/success recover, and both
  login and pairing limiter maps remain at or below capacity under unique keys.

### Workstream B: Bounded Tunnel Source Limiter

Owned files are `internal/tunnel/server.go` and focused Tunnel tests. Changes
outside those files require an explicit planner amendment.

- Keep the existing per-source attempt semantics (twenty per one-minute
  default), but add a hard 4096-entry capacity and one-minute expiry/idle
  reclamation. Cleanup must be synchronous and bounded with no per-entry
  goroutine.
- A new source at capacity evicts the least-recently-observed entry. Existing
  current-window sources still reject at their attempt limit, successful auth
  still deletes its entry, and expired entries are reclaimed before insertion.
- Production time remains supplied by `Gateway.Now`; focused tests call the
  limiter with an injected deterministic time. Tests must prove hard capacity
  under many unique sources, expiry reclamation, LRU replacement, unchanged
  current-source limit/success behavior, and package race safety.

### Revision Merge And Review Gate

After both workstreams freeze disjoint source/tests, one integration owner
writes `summary.md` revision 1 and performs resource-gated, sequential
verification on a complete isolated tree: focused package tests and repeats,
affected-package race, full `go test ./...`, full `go test -race
./internal/...`, `go vet ./...`, and both command builds. Any failure stops the
sequence and is recorded verbatim. An independent reviewer then rechecks only
the required fixes plus regressions and issues a new Task009 decision. Task010
remains blocked until that decision is `APPROVED`.

## Status

Revision 1 is ready for the two bounded implementation workstreams above.
Tasks001-008 are independently approved; Task010 remains blocked on the
subsequent independent Task009 re-review.

## Owner

Two disjoint revision implementers for the authorized files, followed by the
same independent integration reviewer who authored revision 0 findings.

## Goal

Review the assembled MVP as one trust chain, run bounded static/dynamic checks, find cross-package bugs missed by focused reviews, and issue exactly `APPROVED` or `CHANGES_REQUESTED` with reproducible evidence.

## Review Scope

- Entire tracked/untracked MVP source, config, tests and deployment assets.
- All authoritative baselines, ADRs, prior plans/summaries/reviews and known deviations.
- Browser → HTTP/WS/SSE → ownership → Agent approval → strict SSH → Tunnel → Remote execution.
- Startup, unpair, replacement, cancellation, shutdown and external-provider failure transitions.

The revision 0 reviewer did not edit implementation. Revision 1 implementers
may edit only the files explicitly authorized above and must not edit
`review.md`.

## Required Review Lenses

### Security And Authorization

- Every browser Device/Terminal/Agent/Tool resource derives owner from authenticated cookie and DB joins; ID knowledge never authorizes.
- Exact Origin on state mutations and Terminal WebSocket; Secure/HttpOnly/SameSite cookie; standard error envelopes without internals.
- Pairing one-use/expiry/digest/rate-limit and Device challenge/key derivation.
- SSH exact host/client key, username, channel/request allowlist; no insecure callback or TCP listener.
- OpenCode loopback/Basic Auth, wildcard deny plus request tool deny, unique empty workspace, session-bound tool bridge and no local execution fallback.
- Default approval, session-only upgrade, decision races, timeout/tool/output caps.
- Secret/command/terminal-data redaction across logs, UI storage, env examples, errors and test artifacts.
- TLS proxy headers/WebSocket/SSE behavior and no public internal bridge.

### Concurrency And Lifecycle

- Half-authenticated Tunnel publication, newest-wins exact cleanup, heartbeat timeout, reconnect and pre-auth slot release.
- Stream/signer from same Connection instance.
- PTY child process group kill/reap, session/stream close, Terminal limiter release and slow-browser backpressure.
- One-turn/session locking, pending approval cancellation, SSE slow subscriber isolation, OpenCode active-map cleanup/abort.
- Unpair closes Tunnel/Terminal/Agent; shutdown cannot hang on WebSockets/SSE/Tunnel/child process.
- Inspect channel send/close ordering and lock inversions; run race tests in isolated small package groups.

### Protocol And Resource Limits

- Strict JSON/version/type/unknown-field/trailing-input validation.
- Every HTTP/SSE/WS/control/SSH/tool message size/time/count/concurrency bound.
- stdout/stderr capture cannot allocate without bound and correctly marks truncation.
- Database transaction/owner queries and state transitions survive duplicate/concurrent requests.
- Static SPA fallback cannot mask API errors or traverse paths.

### Product Acceptance

- Web routes and payload names align with actual Go handlers.
- Terminal cols/rows orientation and binary/text frame handling align end-to-end.
- Agent canonical event payloads align with reducer/tool cards/decision calls.
- Fake Adapter invokes real injected Remote executor in integrated mode.
- OpenCode event parsing/tool callback aligns with probed 1.18.11 contract and degrades honestly under rate limit.
- README/.env/Compose commands work from a clean checkout.

## Verification

Check memory before each and run sequentially, not as one unbounded command:

```bash
GOMAXPROCS=2 go test -count=1 -p 2 ./...
GOMAXPROCS=2 go vet ./...
GOMAXPROCS=2 go test -count=1 -p 1 -race ./internal/<small-package-group>
npm --prefix web ci
npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
docker compose -f deploy/compose.yaml config
```

Run race tests as several independently monitored package groups. Stop if local available memory falls below 8 GiB or ASD below 3 GiB. Docker build is optional for reviewer if task008 already has fresh evidence, but inspect Dockerfile/Compose manually.

## Decision Rules

Request changes for any issue that can:

- execute on Server rather than selected Remote;
- cross user/device/session ownership;
- bypass approval or strict key verification;
- leak a credential/terminal input;
- publish an unauthenticated/half-authenticated connection;
- leave a remotely triggered unbounded allocation/goroutine/child process;
- break one of the 12 MVP acceptance checks;
- make documented clean build/deploy impossible.

Pure style or Alpha hardening may be recorded as non-blocking follow-up only.

## Deliverable

Write `review.md` with:

- decision;
- findings ordered by severity with exact files/behavior;
- narrow required fixes;
- exact verification evidence and memory observations;
- next action.
