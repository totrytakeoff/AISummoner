---
task_id: task008
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 0
review_required: true
---

# Task 008 Summary

Task008 is ready for independent review. The lifecycle handoff is integrated,
the composition source plus deterministic Go integration tests are
implemented, and the latest merged-tree Go, Web and two-mode Compose gates
pass. The pinned OpenCode image also builds and reports the exact required
version. The production Server image reached its network-dependent Go module
download but could not be completed because ASD-Host timed out reaching
`proxy.golang.org`; both attempts are recorded below without reclassifying the
environmental failure as a pass.

## Lifecycle Workstream Consumed

The composition root consumed the `ready_for_integration` handoff in
`lifecycle_summary.md` without changing its approved implementations:

- `internal/devicegate/` provides one context-aware keyed Device lifecycle
  gate shared by Gateway publication and owner-authorized unpair.
- `internal/store/devices.go` atomically clears ownership, consumes active
  pairing codes, revokes all Device Agent Sessions and terminalizes active Tool
  rows; `internal/store/agent.go` applies the current-owner/non-revoked
  predicate to every owner-facing Agent operation.
- `internal/tunnel/manager.go` and `server.go` split immediate logical detach
  from joined network cleanup, serialize publication with unpair and make
  Gateway/Manager shutdown a joined admission barrier.
- `internal/agent/service.go` and `hub.go` install process-lifetime revoked
  Session tombstones, synchronously initiate Turn cancellation, close SSE and
  join affected Turns without allowing finalizers to overwrite `revoked`.
- `internal/device/service.go` holds the shared gate through commit, Agent mark
  and exact Tunnel detach, then releases it before bounded Tunnel, Terminal and
  Agent joins under a fresh cleanup context.

The exact exported lifecycle boundaries consumed by composition are
`devicegate.New`, `device.NewLifecycleService`,
`tunnel.Manager.DetachDevice`, `terminal.Handler.CancelDevice`, and the Agent
`MarkDeviceRevoked`/`InvalidateDevice` pair. Their focused interleaving, race,
vet and build evidence is recorded in `lifecycle_summary.md`.

## Composition Files Changed

- `cmd/aisummoner-server/main.go`, `server.go`, `server_test.go` — complete
  process construction, startup cleanup ownership transfer, one shared Device
  gate, exact handler composition, signal/run/shutdown ownership, Fake and
  OpenCode selection, real Remote/Terminal/Agent route fixtures and unpair
  invalidation coverage.
- `internal/config/config.go`, `config_test.go` — conditional Fake/OpenCode
  configuration, strict loopback provider/Bridge validation, private directory
  preparation, secret-safe errors and literal-loopback-only public binding
  whenever the Base URL uses development HTTP. HTTPS may still bind broadly
  behind Caddy.
- `internal/staticweb/handler.go`, `handler_test.go`,
  `assets/index.html` — clean-checkout tracked placeholder embed, immutable
  hashed-asset caching, no-cache index/SPA fallback and safe 404/path handling.
- `internal/app/dispatcher.go`, `readiness.go`, `health.go` and tests — exact
  top-level path-shape dispatch using the original `ResponseWriter`, public
  readiness and bounded SQLite health probing without claiming model health.
- `internal/app/runtime.go`, `runtime_test.go` — linearized Run/Shutdown state,
  background-owned total shutdown deadline, idempotent joined callers and
  Agent -> Terminal -> Tunnel -> Bridge -> public HTTP -> SQLite ordering.
  SQLite is intentionally left open if a trusted lifecycle owner misses the
  internal deadline. Only wrapped `net.ErrClosed`/`http.ErrServerClosed` are
  normalized as benign HTTP shutdown results.
- `internal/app/ssh_adapters.go` and tests — Task003 closure adapters for PTY
  and remote exec. Agent capture requests exactly
  `agent.MaxToolOutputBytes+1`; a focused oracle proves a 262145-byte remote
  result becomes capped with `truncated=true` at the Agent boundary.
- `internal/app/opencode_startup.go` and tests — bounded startup capability
  probing; `available` proceeds, while HTTP 429 is classified
  `rate_limited` and other unavailability fails startup without exposing URL,
  credentials or response bodies.
- `internal/app/deploy_contract_test.go` — static clean-checkout oracles for
  image pinning, topology, health GET, build context exclusions, silent Compose
  validation, private `.env` creation and private pairing-code output.
- `deploy/Dockerfile`, `OpenCode.Dockerfile`, `compose.yaml`, `Caddyfile`,
  `validate-compose.sh`, `aisummoner-client.service` — deterministic Web/Go
  build, exact `opencode-ai@1.18.11` sidecar build, same-network-namespace
  loopback topology, Caddy-only published ports, silent secret-safe Compose
  validation and non-journal pairing-code capture.
- `.dockerignore`, `.gitignore`, `.env.example`, `Makefile`, `README.md` —
  defense-in-depth build exclusions, resource-bounded commands, accurate
  Fake/OpenCode setup, reserved Session-secret semantics, mode-0600 `.env`
  creation and operational pairing-code handling.

No WebUI product logic was changed; production embed glue lives in the
multi-stage Docker build and replaces the tracked placeholder only inside the
Go build stage.

## Constructed Server And Route Behavior

`buildServer` constructs resources in this order:

1. validated/private configuration and SQLite migrations;
2. Auth/bootstrap and Pairing;
3. one `devicegate.Gate`, Tunnel Manager and Gateway using that gate;
4. strict SSH Dialer, Terminal PTY closure and Agent remote executor;
5. Fake Adapter, or a pre-bound loopback Bridge plus bounded healthy OpenCode
   Adapter;
6. Agent Service/API and lifecycle-aware Device Service using the same gate;
7. Browser API, health/readiness, static handler and exact dispatcher;
8. a pre-bound public listener and the sole owning `app.Runtime`.

Startup failures unwind a LIFO cleanup stack. After `Transfer`, Runtime is the
only database/listener/domain owner; there is no post-transfer database or
Tunnel defer in `run`. Startup logs only the fixed message `server ready` and
do not record listen/Base URL/provider configuration values.

The dispatcher preserves the original writer so Tunnel/Terminal WebSocket
Hijack and Agent SSE ResponseController/Flush reach the real network writer.
Internal OpenCode Bridge paths are never mounted publicly. Fake mode neither
reads OpenCode-only configuration nor binds a Bridge listener. OpenCode mode
binds the separate Bridge before Adapter construction and health probing.

Default development endpoints are public Server `127.0.0.1:8080`, OpenCode
`127.0.0.1:4096` and Bridge `127.0.0.1:4097`. Compose uses Server port 8080
only inside the private network namespace, OpenCode/Bridge loopback only, and
publishes only Caddy 80/443.

## Deterministic Composition Coverage

The real `buildServer(fake)` fixture verifies through the actual public
listener and dispatcher:

- health, placeholder/static SPA, API JSON miss separation and real login;
- a real Tunnel upgrade and a real Remote Client with persistent Device
  identity, challenge/proof, pairing claim, yamux and Embedded SSH Server;
- a real Terminal WebSocket and PTY command whose split input cannot satisfy
  the output sentinel via terminal echo;
- a `full_access` Agent Session, immediate SSE flush, and Fake Adapter's real
  SSH-backed `hostname` and `uname -a` calls, including actual local Remote
  hostname evidence in the persisted Snapshot;
- shutdown while Tunnel, Terminal and SSE are still open: Server Runtime must
  join first, Manager must be offline and browser peers must observe closure
  before the Remote reconnect loop is canceled.

The production DELETE-unpair fixture reuses the same real Remote identity and
holds reconnect at a deterministic Jitter barrier. Before releasing reconnect,
it proves `DELETE /api/v1/devices/{id}` returns 204, Manager is offline, the
owner Device surface is 404, Terminal and SSE are closed by the Server and the
old Agent Session rejects both Snapshot and new Turn with 404. Releasing the
barrier lets the same identity reconnect, receive a non-empty one-time code
different from the first, and remain unowned until a new claim.

OpenCode root-composition fixtures prove health runs after the private Bridge
listener is bound but before public readiness, the private callback is
reachable only on its loopback listener, the public callback path is a JSON
404, and rate-limited/unavailable startup releases Bridge, public listener and
SQLite ownership. Source oracles require the shared gate, startup probe and
all three exact Runtime closers so a correct Tunnel closer cannot mask a
no-op Terminal or Agent root connection.

## Deployment And Secret Boundaries

- `deploy/OpenCode.Dockerfile` uses fixed Node 22.18.0 and installs/asserts
  exactly `opencode-ai@1.18.11`; Compose uses only the fixed local image
  `aisummoner-opencode:1.18.11` and has no unverified override/latest path.
- OpenCode uses `network_mode: service:server`; Server and sidecar share the
  workspace volume and numeric UID/GID 10001, while the SQLite volume remains
  Server-only. Caddy is the sole service with published ports.
- `validate-compose.sh` rejects arguments and runs only `docker compose ...
  config --quiet`; it never renders interpolated passwords/secrets to stdout.
- README creates `.env` with `install -m 600` and checks mode 600 before
  Compose. `.dockerignore` is explicitly documented as defense-in-depth, not a
  sole secret boundary.
- Remote systemd output uses a private 0700 StateDirectory, `UMask=0077` and
  `StandardOutput=append:/var/lib/aisummoner-client/pairing-output.log` rather
  than the journal. README requires mode/owner verification and secure
  truncate immediately after claim.
- `AISUMMONER_SESSION_SECRET` remains validated only because it is a frozen
  Baseline-0 minimum configuration value. It is accurately documented as
  reserved: current Sessions are independent random opaque tokens and SQLite
  stores only SHA-256 digests.

## Verification Completed So Far

All Go verification used ASD-Host isolated staging, `GOMAXPROCS=2`, explicit
package parallelism and a resource/no-competing-Go gate. The local host does
not have `gofmt`; formatting was performed in staging, synchronized back and
checked by SHA-256 plus `git diff --check`.

### Composition focused and repeat

- Initial focused snapshot:
  `go test -count=1 -p 1 -timeout 180s ./internal/config ./internal/staticweb ./internal/app ./cmd/aisummoner-server`
  — PASS (`0.005s`, `0.012s`, `0.066s`, `2.432s`; wall 7s).
- After HTTP-listener, log-redaction, Runtime-closer and deployment permission
  oracles, the same focused command — PASS (`0.005s`, `0.006s`, `0.066s`,
  `2.422s`; wall 4s). Pre/post MemAvailable was 5,485,228/5,388,056 kB and
  SwapFree remained 2,062,420 kB.
- First four-package `-count=5` — FAIL in three Runtime tests because a
  listener-close race returned wrapped `net.ErrClosed` from
  `http.Server.Shutdown`; all other package runs passed. Production was
  narrowed to suppress only the two canonical closed-server conditions.
- Focused `go test -count=1 -p 1 -timeout 180s ./internal/app` after that fix —
  PASS (`0.068s`; wall 1s).
- Corrected four-package `-count=5` — PASS: config `0.013s`, staticweb
  `0.008s`, app `0.322s`, Server `11.557s` (wall 14s).

### Merged-tree Go gates completed before the pending redirect revision

- Fresh staging contained 82 `cmd/`/`internal/` Go files with no `gofmt`
  difference and matching local/remote manifest. Focused race:
  `go test -race -count=1 -p 1 -timeout 600s ./internal/config ./internal/staticweb ./internal/app ./cmd/aisummoner-server ./internal/tunnel`
  — PASS (`1.019s`, `1.030s`, `1.162s`, `4.374s`, `2.713s`; wall 18s).
- First full `go test -count=1 -p 2 -timeout 900s ./internal/... ./cmd/...`
  — FAIL only because clean staging omitted root `opencode.json`; the OpenCode
  canonical-template test could not open it. This was a staging setup failure,
  not a source assertion. After adding root `opencode.json` and
  `.opencode/tools/remote_exec.ts` with matching hashes, the identical command
  passed all 23 packages (wall 11s). Pre/post MemAvailable was
  5,483,724/5,205,944 kB; SwapFree remained 2,062,420 kB.

### Real DELETE-unpair root regression

- Focused single run — PASS (`0.577s`; wall 1s), with no residual Go process or
  timeout/goroutine symptom.
- The same exact test with `-count=10` — PASS (`4.961s`; wall 5s).
- `go test -race -count=1 -p 1 -timeout 300s ./cmd/aisummoner-server` — PASS
  (`5.437s`; wall 7s). Pre/post MemAvailable was 5,479,116/5,406,940 kB and
  SwapFree remained 2,062,420 kB.

### Historical merged-tree gate before Task003 revision 4

- After the approved Tunnel redirect pinning revision, a fresh complete-repo
  staging tree contained root canonical OpenCode templates, deployment and
  documentation files, `.dockerignore`, `.env.example`, and Web source/lock
  files. Local/remote SHA-256 matched for 136 checked files with manifest
  `e45d17294dd7a5bc7be1f303f4697e22763c6176d86ac8bff91bc53a5d19ffcc`;
  `gofmt -l` was empty and `git diff --check` passed.
- `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 900s ./...` — PASS all 24
  targets (wall 14s). Pre/post MemAvailable was 5,470,460/5,294,108 kB and
  SwapFree remained 2,062,420 kB.
- `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 1200s ./internal/...`
  — **FAIL** only in `internal/sshclient/TestTunnelSSHEndToEnd`. The race
  detector observed `internal/sshserver.(*process).terminate` closing the PTY
  `os.File` at `server.go:520` while `sessionState.resizePTY` called
  `pty.Setsize`/`File.Fd` at `server.go:270`. Nineteen other internal packages
  passed; wall time was 61s. Pre/post MemAvailable was
  5,478,868/5,338,696 kB and SwapFree remained 2,062,420 kB.
- The gate stopped immediately. Vet and build were not started. This is a real
  Task003 SSH/PTTY lifecycle race outside the composition-owned files and must
  be repaired and independently reviewed before Task008 final verification can
  continue.

### Latest merged-tree final gate after Task003 revision 4

Task003 revision 4 independently passed review before this gate. A new complete
staging tree `/tmp/aisummoner-task008-finalgo3.eOjiTn` explicitly retained all
root canonical OpenCode templates, deployment/documentation files,
`.dockerignore`, `.env.example`, static placeholder and Web source/locks. The
136-file local/remote manifest matched at
`8b2db79a12805648bdeff8fe3a500073a2cd1cf12767c9d73693cba6adfe0101`;
asset checks passed, `gofmt -l` was empty and `git diff --check` passed.

- `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 900s ./...` — PASS all 24
  targets (wall 15s). Pre/post MemAvailable was 5,444,408/5,101,448 kB.
- `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 1200s ./internal/...`
  — PASS all 21 internal packages (wall 61s), including the exact SSH client
  composition that previously exposed the PTY race. Pre/post MemAvailable was
  5,440,576/5,326,016 kB.
- `GOMAXPROCS=2 go vet ./...` — PASS with no output (wall 1s).
  Pre/post MemAvailable was 5,446,500/5,410,052 kB.
- `GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client`
  — PASS with no output (wall 2s). Pre/post MemAvailable was
  5,475,768/5,396,908 kB.

SwapFree remained 2,062,420 kB throughout and no residual Go tool process was
reported. During the initial complete tar/check sequence one independent SSH
connection timed out. No Go command had started; asset, format, hash and diff
checks were all rerun to explicit success before the gate proceeded.

### Web clean-install, test and production build

These commands ran sequentially only in the verified ASD-Host final Go staging
tree. They did not create `node_modules` or `dist` in the local working tree.
Node was v20.19.2 and npm was 9.2.0; every test/build used the approved 2 GiB
Node heap cap.

- `npm --prefix web ci` — PASS, added 167 packages and audited 168 with zero
  vulnerabilities (wall 4s). npm emitted one deprecation warning for the
  transitive `whatwg-encoding@3.1.1`. Pre/post MemAvailable was
  5,489,472/5,279,732 kB.
- `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run` —
  PASS all 8 test files and 22 tests (Vitest 5.01s; wall 7s). Pre/post
  MemAvailable was 5,323,684/5,257,380 kB.
- `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build` — PASS
  TypeScript and Vite production build, transforming 64 modules (wall 7s).
  `dist/index.html` is non-placeholder and both emitted assets have hashed
  names. Vite reported the 557.23 kB minified JavaScript chunk exceeds its
  500 kB advisory threshold (155.90 kB gzip); this is a recorded MVP
  optimization residual, not a build failure. Pre/post MemAvailable was
  5,353,628/5,204,124 kB.

SwapFree remained 2,062,420 kB for all Node stages.

### Secret-safe Compose validation

Docker Compose v5.4.0 validation ran locally without starting or building any
container. Two separate temporary environment files were mode 0600 and used
only conspicuous non-production sentinel values, including at least 32-byte
Session, Pairing and Bridge secrets. The script consumed them through
`AISUMMONER_ENV_FILE` and the temporary directory was deleted afterward.

- Fake Adapter with empty `COMPOSE_PROFILES` — PASS, exit 0, zero bytes on
  stdout/stderr and no sentinel output.
- OpenCode Adapter with `COMPOSE_PROFILES=opencode` — PASS, exit 0, zero bytes
  on stdout/stderr and no sentinel output.

The local host started with 7,113,180 kB MemAvailable and 9,446,972 kB
SwapFree; it ended with 6,999,644/9,319,120 kB respectively. The first Fake
wrapper attempt used zsh's read-only variable name `status` after Compose had
returned, so it did not produce a trustworthy result. Renaming only that shell
variable and rerunning the identical validation produced the recorded pass;
no secret value was printed in either attempt.

### Docker image gates

Docker 26.1.5 and Buildx 0.13.1 ran on ASD-Host only after confirming more
than 3 GiB available memory, no competing Go/Node/Docker build process and
adequate Docker-root capacity. No Compose service or existing container was
started, stopped or modified. The build context contained no runtime secret.

- `docker build --progress=plain -f deploy/OpenCode.Dockerfile -t
  aisummoner-opencode:1.18.11 .` — PASS (wall 60s, no OOM abort). The retained
  image is
  `sha256:9d13dd1062fac8e921fd20910104de4ba10abe92612191e449b8cc14e612a174`,
  size 584,064,069 bytes, user `10001:10001`, working directory
  `/var/lib/aisummoner/workspaces` and entrypoint `opencode`. An isolated
  `--rm --network none` invocation returned exactly `1.18.11`. Monitored
  MemAvailable stayed between 5,077,320 and 5,360,480 kB; final SwapFree was
  2,006,140 kB. npm emitted only its version notice and the expected
  `npm cache clean --force` warning.
- The first production Server-image build ended without creating
  `aisummoner-server:mvp0`. The tool session carrying plain progress was lost
  across context compaction, so its exact exit line was unavailable. Read-only
  BuildKit cache inspection proved `npm ci` had completed while Web build,
  `go mod download` and runtime package stages had not committed; daemon logs
  showed no OOM event. This attempt is recorded as failed, not passed.
- Before the one authorized mechanical retry, the exact 127 files consumed by
  the Dockerfile matched between the working tree and isolated staging at
  aggregate SHA-256
  `b7cddb2a0a2e31a06a4073bbf179d560ad19c140f029e6dd643316c0923a58eb`.
  The retry used `set -o pipefail`, retained full plain progress and monitored
  memory every 15 seconds. It failed at Dockerfile line 20 after 120.3 seconds:
  `go mod download` timed out connecting to `proxy.golang.org` for
  `github.com/coder/websocket@v1.8.14`; overall exit was 1, wall 135s and
  `OOM_ABORT=0`. Runtime `apk add` was canceled by BuildKit after that failure.
  Final MemAvailable/SwapFree was 5,380,692/1,990,460 kB. Per the fail-stop
  gate, no further retry or Server-container smoke was run.
- Final freeze reconfirmed the Server tag is absent, retained and rechecked the
  OpenCode tag/version, found no residual build/Go/Node job, then deleted the
  exact staging tree and its network-failure log. ASD-Host ended with
  5,428,776 kB MemAvailable and 2,058,556 kB SwapFree.

Three revised-focused preflight shell attempts failed before launching Go:
one remote hash command had an escaped-AWK error, one zsh scalar was not split
into file arguments, and one resource-print AWK expression was expanded under
`set -u`. Each was corrected, hashes were subsequently identical and no test
result was hidden or reclassified. The first full-Go staging omission is also
recorded above.

The first complete-repo final staging tar used a broad `.env.*` exclusion that
also omitted the non-secret `.env.example`; the asset gate failed before any Go
command. The explicit allowed example was restored, all required assets and
hashes then passed, and final Go testing used that verified staging tree.

## Reviewer Handoff

- Review the actual composition and deployment sources against the plan; do
  not infer a Server-image pass from the successful direct Go/Web gates.
- Task010 must either retry the unchanged production Server Dockerfile from a
  host that can reach `proxy.golang.org`, or use the already verified direct
  Server/Client binary path for controlled deployment. It must still perform
  the dynamic image/runtime and three-host acceptance checks.

## Deviations And Residuals

- The approved lifecycle amendment split Tunnel logical detach from joined
  network close and made Manager/Gateway shutdown joined. This preserves the
  Device gate's non-blocking authorization linearization and is documented in
  `lifecycle_summary.md`.
- Runtime now explicitly treats only canonical closed-listener/server errors
  as normal shutdown; this was discovered by deterministic repeat and does not
  weaken non-benign HTTP shutdown reporting.
- OpenCode image construction and exact-version execution are claimed above;
  production Server image construction is not. Static topology and secret
  oracles are present. Task010 remains responsible for retrying or bypassing
  the environment-blocked Server image gate during controlled deployment and
  for the dynamic 0600 pairing-output check.
