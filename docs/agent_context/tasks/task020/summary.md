---
task_id: task020
type: summary
status: in_progress
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 020 Summary: DSH-First Agent Runtime

## Current Outcome

The DSH-first implementation, reproducible Linux runtime package and bounded ASD
test deployment are complete. The deployed Controller is available at
`https://122.51.70.33:10001`, serves the current Chinese Workspace bundle over a
publicly trusted certificate, and supervises a pinned private DSH Host plus the
AISummoner capability bridge on loopback-only listeners.

Task020 intentionally remains `in_progress`: the private DSH credential store is
empty. The remaining acceptance step requires the human tester to enter a
DeepSeek API key through the authenticated Controller settings and complete one
real Browser -> DSH -> approval -> Remote SSH tool -> streamed final-answer
Turn. The key must not be placed in chat, shell history, deployment environment,
AISummoner SQLite or this document.

## Implemented Boundaries

- `internal/dsh` owns the pinned Host process, private runtime-home overlay,
  strict bounded RPC/EventStream adapter, external Session create/resume,
  reasoning/final projection, cancellation and joined shutdown.
- The embedded `aisummoner` DSH preset disables DSH-local shell, filesystem,
  search, subagent and workflow tools. Its only capability is the authenticated
  AISummoner Remote execution bridge.
- The generalized loopback HMAC bridge binds callbacks to the exact active
  product Turn and external DSH Session. DSH cannot select a Device; approval,
  current ownership/online checks, timeout, output limits and SSH identity stay
  product-owned.
- `dsh` is an exact Server adapter mode. Fake mode ignores DSH-only settings.
  DSH Host and bridge URLs must be literal loopback endpoints, runtime paths
  must be absolute, the home must be private, and the shared secret remains
  bounded and write-only.
- The authenticated, exact-Origin `POST /api/v1/agent-provider/dsh` endpoint
  writes a DeepSeek API key only through DSH's credential API and never returns
  it. New Sessions bind to DSH; already-persisted provider bindings are kept.
- The Chinese Controller settings now present DSH as the first-class runtime,
  use a write-only password input, and accurately describe Server-side storage.
  The accepted single `组件` launcher and dock-owned `终端` / `设备` tabs remain
  unchanged.
- Packaging is pinned to DSH `0.1.0-rc.5` at commit
  `47f943859bef60e4160492346772ded9b24f765a`, Node `24.19.0`, pnpm `11.7.0`
  and a digest-pinned Debian Bookworm builder. Third-party licence/source
  information is recorded in `THIRD_PARTY_NOTICES.md`.

## Verification

### Go and Web

- Fresh isolated ASD focused Go packages — PASS.
- DSH/bridge focused tests at `-count=10` — PASS.
- Focused race packages — PASS.
- Full `go test ./...` — PASS.
- Full `go test -race ./internal/...` — PASS.
- `go vet ./...` — PASS.
- Server and Client command builds — PASS.
- Web Vitest — PASS: 16 files, 67 tests.
- Web TypeScript/Vite production build — PASS. The deployed assets are
  `index-uoeQ9N-W.js` and `index-DJxk0biW.css`.
- Production Server candidate SHA-256:
  `adefee051d44f0dd5da91c7836397fee52a444f61de496477b3fcd277d447274`.

All heavyweight gates were serial. Go used `GOMAXPROCS=2` and bounded package
parallelism; Node used one worker and a 2 GiB heap; the package builder used two
CPUs, 3 GiB memory, 4 GiB memory+swap and 512 PIDs. No OOM occurred and no
unrelated process or container was terminated.

### Pinned Runtime Package

- Archive:
  `/home/myself/workspace/.aisummoner-package/aisummoner-dsh-runtime-linux-x64.tar.gz`
- SHA-256:
  `40685f7cce3eac497a707aab097ef5c398fedb098c7b99016f0da7d3de4e9018`
- Size: `117419895` bytes.
- Archive checksum, Node `24.19.0`, DSH `0.1.0-rc.5`, dependency imports,
  private credential write mode and joined Host shutdown — PASS.
- The rebuilt `node-pty` native module requires at most GLIBC `2.34`, compatible
  with the ASD runtime.
- A real packaged Host smoke proved `host.describe`, private `0600` credential
  writing, the AISummoner preset, Session creation and exact joined close.

### ASD Deployment

- The existing scoped Server unit remains active with PID `797585`, uid `1001`,
  `NRestarts=0`, and the exact candidate binary hash above.
- Its owned DSH child is PID `797597`, uid `1001`, using the packaged Node
  executable. `host.describe` returns HTTP 200 with a valid success envelope.
- Public strict TLS health is HTTP 200 with certificate verification result 0;
  the index references both current asset names. An exact-Origin unauthenticated
  DSH configuration request returns 401. Missing/wrong Origin returns 403
  before authentication by design.
- Server, DSH Host and capability bridge listen only on `127.0.0.1:8088`,
  `127.0.0.1:14196` and `127.0.0.1:14197`. Retired bridge port 14097 is free.
  The existing OpenCode sidecar remains on loopback 14096. Public Caddy alone
  listens on TCP 10001.
- The credential file is absent, as required before human setup. Server warning
  count since the switch is zero.
- All six nginx configuration hashes remain unchanged. The existing Caddy,
  OpenCode and four unrelated ASD containers remain running under their
  original names; none was restarted, edited or removed.
- ASD post-deploy memory remained about 4.5 GiB available with about 2.0 GiB
  swap free.

## Honest Failure And Retry Record

1. Early release metadata verification used an invalid shell expression and
   stopped before producing a package.
2. Native dependency installation first attempted a network `node-gyp` header
   download and timed out; the builder now uses the verified pinned Node
   headers.
3. Successive offline package probes exposed missing runtime peers, deploy
   metadata and recursive required peers. Each missing closure was added to the
   deterministic materializer before the next probe.
4. The initial host-native archive contained a `node-pty` binary requiring
   GLIBC `2.42`; ASD rejected it. That archive was deleted exactly and replaced
   with the Bookworm-built artifact.
5. The first two Bookworm packages omitted `pty.node`: pnpm's shared-store
   lifecycle behavior made `pnpm rebuild node-pty` a no-op for the transitive
   package. No deployment used either archive.
6. A third package copied the existing store artifact, still requiring GLIBC
   `2.42`. A targeted probe then deleted the exact build directory and invoked
   the pinned bundled `node-gyp` directly; the resulting module imported and
   required only GLIBC `2.34`. The fourth formal package passed.
7. The first manual Host smoke incorrectly expected the DSH protocol version
   (`0.0.1`) to equal the CLI package version (`0.1.0-rc.5`) and timed out. The
   corrected structural nonempty-version oracle passed; production already used
   that correct contract.
8. One Web date-sensitive fixture failed despite correct production behavior.
   A first fake-timer workaround timed out twice; an injected `now` seam made
   the final 67-test suite deterministic.
9. Running the ASD package checker through `runuser` initially inherited an
   inaccessible root working directory. Re-running from `/tmp` as uid 1001
   passed without changing the package.
10. An early post-deploy probe used unavailable `jq`; another local wrapper had
    a shell-quoting failure. Both stopped before mutation. Fixed, value-safe
    probes then verified service, process, listener, endpoint and hash state.
11. A post-deploy request without Origin returned 403. Source and existing tests
    confirmed the exact-Origin check intentionally precedes authentication;
    using the deployed exact Origin and no cookie returns the required 401.
12. A local raw TCP isolation probe was invalid because the workstation routes
    the target through a transparent `FlClash` TUN and falsely reported every
    probed port reachable. It is not accepted as evidence. ASD's owning-socket
    inspection proves the private services bind only literal loopback; a later
    independent lzr SSH probe was unavailable due to an SSH timeout.

## Remaining Acceptance Gate

1. Human tester opens `https://122.51.70.33:10001`, signs in, opens
   `设置 -> Agent 与模型`, enters the DeepSeek key locally and selects
   `保存密钥`. Only a success/failure result may be reported; never the value.
2. Create a new DSH Session for the paired online Device, send a benign prompt
   that requires `hostname` and `uname -a`, approve the tool call, and verify
   ordered reasoning/final separation plus the Remote Device result.
3. Resume the same Session and run one cancellation check. Confirm the DSH Host,
   bridge, Server and unrelated ASD services remain stable.
4. Once the real credentialed E2E passes, freeze the final exact hashes and move
   this summary to `ready_for_review` for independent review.

## Core Hashes

- `internal/dsh/adapter.go` `1a367869...0af13c`; `host.go`
  `5da04ae7...213c2`; `assets.go` `979958ca...c6ea`.
- `internal/opencodebridge/bridge.go` `4122057a...42d0`;
  `internal/config/config.go` `0dac9999...0673`;
  `cmd/aisummoner-server/server.go` `0ee1462d...8ec`.
- `web/src/components/ControllerSettingsDialog.tsx`
  `c4f6c827...2a44`; `web/src/agent/experience.ts`
  `27c9fc85...3771`.
- `deploy/package-dsh-runtime.sh` `62d6d240...6f6b`;
  `deploy/package-dsh-runtime-container.sh` `fb3851b7...5dfe`;
  `deploy/materialize-dsh-runtime.mjs` `060d5493...c87`;
  `deploy/check-dsh-runtime.sh` `28704d83...2f`.
- `THIRD_PARTY_NOTICES.md` `35f4a698...2350`; `README.md`
  `03a7fa1b...32f8`.
