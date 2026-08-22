---
task_id: task009
type: summary
status: ready_for_review
from: revision_coders
to: task009_reviewer
revision: 2
review_required: true
---

# Task 009 Summary — Revision 2

Revision 1 closed both original unbounded-resource findings and passed the
complete merged Go gate, but independent review found that production Caddy
made direct `RemoteAddr` a shared proxy key. Revision 2 now implements the
trusted exact-peer/dedicated-overwrite-header boundary in plan revision 2.
Both disjoint workstreams are frozen and the fresh merged tree passed focused,
repeat, race, complete-test, vet, command-build and secret-safe Compose gates.
This summary requests independent review; it does not approve the revision.

## Revision 2 Files Changed

Workstream C owns these nine files; final local and isolated-ASD SHA-256 values
matched exactly:

- `internal/requestsource/resolver.go`
  (`db3ee1f3bae2c7cde3dd106ff6234cc8f6895f4db8de3118efebfefa437f78e4`)
  — immutable exact-peer resolver and fixed safe error.
- `internal/requestsource/resolver_test.go`
  (`f017bf57ffd6c5459216ee4356f3b54daffc093536bfcabf77e3c766fa0d9e74`)
  — canonical trusted/untrusted, malformed and anti-spoof coverage.
- `internal/config/config.go`
  (`3fa820c7f0104135eb3f03fccd09b750dfb54af78b471f01727a1159cb8b32f0`)
  and `internal/config/config_test.go`
  (`4de7a81d63bdbb158a67f8ded5a65238a5f71f98a0afa4478e6eba50c877445c`)
  — exact literal trusted-proxy list parsing and rejection without value echo.
- `internal/app/deploy_contract_test.go`
  (`02c231d3af9f0c9c2f9526fa5e16613a9fbef7ab2ecf2c4fcf91edb8ac3466cf`)
  — deployment topology/overwrite-header contract.
- `deploy/Caddyfile`
  (`d3a9305b9c280c2a77767444df8c035f7bb07fb13ec852af102da1179d2f57b1`),
  `deploy/compose.yaml`
  (`69d4a7fa33a50b7bc0a0cfce83bf790c8bb0bce4a1e1948c87f0b85728ae9c14`),
  `.env.example`
  (`a1a7b46de466076e8b67a72b6208e54df5d4105bdb9b320b6047c5c19224167f`)
  and `README.md`
  (`762bc70e33bd4f0d75fcc2fabd91a0dcb6fe30d4f91c05c5e8758f17440880a0`)
  — Caddy overwrite, deterministic private IPv4 topology and operator contract.

Workstream D owns these seven files; final local and isolated-ASD SHA-256
values matched exactly:

- `internal/httpapi/api.go`
  (`8a248fa9503c9f88dd36682f462361a3c5bc1faa19cba14cd538d489c2eae1cf`),
  `internal/httpapi/handlers.go`
  (`d123e58988dd9c0657d0bb938b839b9d8495b4449293585177bee4d3c00b2a4b`)
  and `internal/httpapi/api_test.go`
  (`84d50130cd10375c585355a4e4f3b49c5bd4ecb65300ddcf1a8590d4f2660850`)
  — narrow resolver injection plus Login/Pairing fail-closed and source-isolation
  regressions.
- `internal/tunnel/server.go`
  (`633c89247df3837bdd3720cd2583d116d4e6c3067cc23ca82b6024a6937bf214`)
  and new `internal/tunnel/source_resolver_test.go`
  (`fac8b3a7e755a01a552e86aef7b05700f9b42437b43be61b1f5d47a218c7d1d3`)
  — resolver-before-admission, source separation, anti-spoofing and complete
  source/header log redaction.
- `cmd/aisummoner-server/server.go`
  (`4ba27a0e48b62c1cb52eafc6c1d72d97d496c8d00a7bd7542c3353c3264d3cde`)
  and `cmd/aisummoner-server/server_test.go`
  (`bd836d29d47554ca8a3a491e148f43de5c71f07cccbb05a1efdc201bbaafd76a`)
  — construct exactly one Resolver and inject that exact variable into Browser
  and Tunnel.

## Revision 2 Behavior

- `requestsource.Resolver` trusts `X-AISummoner-Client-IP` only when the
  canonical immediate peer is one exact configured IP. A trusted request must
  carry exactly one canonical literal unicast client IP. Missing, repeated,
  comma-separated, whitespace-ambiguous, zoned, noncanonical, unspecified,
  multicast, hostname or host:port values fail with one fixed error.
- Untrusted peers ignore the dedicated header, `X-Forwarded-For`, `Forwarded`
  and every other proxy claim and use only their canonical immediate address.
  Resolver construction copies the validated address slice and is immutable.
- Browser Login and authenticated Pairing Claim resolve before their limiter
  operations. Failure returns the standard request-linked
  `400 INVALID_REQUEST` envelope without invoking Login/Claim or changing
  limiter state. Tunnel resolves before its limiter and pre-auth slot and uses
  a fixed `400 invalid request` response on failure.
- Server composition constructs one resolver from
  `Config.TrustedProxyIPs` and passes the same pointer to Browser and Tunnel.
  Package constructors retain safe direct-peer defaults when no resolver is
  supplied.
- Resolved source IPs, the dedicated header and resolver/config values are not
  written to application logs. Tunnel authentication and protocol-close logs
  retain only safe category/device/connection fields.
- Caddy overwrites the dedicated header from its observed remote host. Compose
  assigns exact configurable Server/Caddy IPv4 addresses on a private edge
  subnet and configures Server to trust only the exact Caddy address. Direct
  development continues with an empty trusted list.

## Revision 2 Focused Workstream Evidence

### Workstream C

- The first transfer attempt used `rsync`; ASD does not provide it, so that
  command exited 12 before compilation. A complete `tar` transfer was then
  used.
- First exact count-one command over `requestsource`, `config` and `app`:
  `requestsource` **FAIL** in 0.003s while `config` **PASS** in 0.006s and
  `app` **PASS** in 0.067s. Two test fixtures populated `http.Header` by raw
  map literal with a noncanonical key, bypassing net/http canonicalization;
  production `Header.Set`/`Add` and production code were unaffected. Only the
  three fixture assignments changed to `Header.Set`.
- Identical retry: **PASS** in 0.004s / 0.006s / 0.069s.
- Seven exact regressions across the three packages with `-count=20`:
  **PASS** in 0.009s / 0.024s / 0.008s.
- Full three-package race: **PASS** in 1.012s / 1.022s / 1.170s.
- Gates observed 5,326--5,351 MiB MemAvailable and 2,010 MiB SwapFree with no
  competing Go process. `/tmp/aisummoner-task009c.I01r2x` was deleted and
  confirmed absent.

### Workstream D

- Count-one over `httpapi`, `tunnel` and Server composition: **PASS** in
  5.065s / 1.785s / 2.927s.
- Eight exact trusted-source, anti-spoof, malformed-source, redaction and root
  wiring regressions with `-count=10`: **PASS** in 4.390s / 0.038s / 0.005s.
- Full three-package race: **PASS** in 38.385s / 9.524s / 5.230s.
- The first wrapper referenced unavailable `/usr/bin/time` and exited before
  Go started. Separately, one race invocation naturally completed but the
  client tool lost its output channel; it was deliberately not accepted. The
  single mechanical rerun produced all three explicit `ok` results above.
- Gates remained above 4.7 GiB MemAvailable with about 2.0 GiB SwapFree and no
  competing/residual Go process. `/tmp/aisummoner-task009-d.aoVnQV` and its
  non-secret race output were deleted and confirmed absent.

## Revision 2 Final Merged-Tree Verification

The integration owner created fresh private ASD staging
`/tmp/aisummoner-task009-r2-final.SFoY08`, excluding `.git`, real `.env`, data,
databases, coverage, binaries, Web `node_modules` and `dist`. It retained
`.env.example`, `.dockerignore`, README, root/OpenCode canonical assets,
deployment files, migrations, static placeholder and Web source/lock files.

- The staged tree contained 190 files and 87 Go files. Remote `gofmt -l` over
  every Go file returned no output. A first aggregate manifest comparison used
  different locale ordering and produced different aggregate hashes even
  though file-by-file diff was empty; repeating both sides under `LC_ALL=C`
  produced the identical sorted-manifest SHA-256
  `7a636a77f0307023584c1c6fc0a760a2fbcd0b2ad338eb0682b546be25a93cdf`.
  Pre-test `git diff --check` passed.
- `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 600s ./...` — **PASS** all 25
  targets (24 test packages plus migrations with no tests). Notable revised
  packages: requestsource 0.004s, HTTP 5.384s, Tunnel 1.841s and Server 2.914s.
- The first complete internal race naturally ran to completion, but the client
  output channel retained only seven package lines; it was not accepted.
  Mechanical rerun used remote `pipefail`, `tee` and a separate result file:
  `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 900s ./internal/...` —
  **PASS**, result file `0`, exactly 22 `ok` lines, no FAIL/timeout. HTTP passed
  in 38.240s and Tunnel in 9.714s.
- `GOMAXPROCS=2 go vet ./...` — **PASS**, no output.
- `GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server
  ./cmd/aisummoner-client` — **PASS**, no output.
- ASD gates stayed around 5.45 GiB MemAvailable with 2.06 GiB SwapFree and no
  competing/residual Go tool. The final observation was 5,469,628 kB
  MemAvailable and 2,058,556 kB SwapFree.

On the local host, Docker Compose 5.4.0 was available with 15,248,868 kB
MemAvailable and 4,159,284 kB SwapFree. Independent mode-0600 placeholder env
files validated Fake and OpenCode modes through `deploy/validate-compose.sh`:
both returned 0 with exactly zero stdout and stderr bytes. No image was built
and no container was started. The placeholder env/output files and their exact
temporary directory were deleted and confirmed absent.

After this tested manifest was created, the planner amended only Task010's
future acceptance documents: `docs/agent_context/tasks/task010/plan.md`
(`b4a0fa660fd3154e5ca277e70c179db50b643419b14b141a72ef97ad5ba6d997`)
and `preflight.md`
(`d90d25c63020a7c17c383e999959eae07f8d7d86d7220a0d52dd69068cee099c`).
They are deliberately recorded as post-manifest documentation, not as source
claimed by the Go test run; local diff checking covers them.

## Retained Revision 1 Evidence

Revision 0 found two remotely triggerable unbounded resource surfaces. Two
disjoint implementation workstreams have now bounded those surfaces, and the
merged complete tree passes focused, repeat, race, full-test, vet and command
build gates. This summary requests independent re-review; it does not approve
the revision.

## Files Changed

Workstream A owns five Auth/HTTP files:

- `internal/auth/service.go`
  (`63b5f7284bdc3f32b634a9da5358760055338cff4a8388564986cbb673025273`)
  — process-global password-verification admission and safe typed errors.
- `internal/auth/service_test.go`
  (`9a4f06479b829582b91b99c3592e79ee16d363902a3d653f46b0e8f5b6536ff7`)
  — deterministic multi-Service verifier barriers, cancellation, panic/error
  release and recovery coverage.
- `internal/httpapi/api.go`
  (`e30d8cb9bfe7d4516f4a41844b0b5baa29b63a8fc1500bd4ae44bb81f8c9c017`)
  — narrow Auth interface plus the shared bounded failure-limiter
  implementation.
- `internal/httpapi/handlers.go`
  (`76d1af23767fca8c45c541e89428dc744be0cd0bdaea866ff775221159e5efe6`)
  — login admission responses and failure/success accounting.
- `internal/httpapi/api_test.go`
  (`5b8d84081ca1c290b770e662537402abcb57b67ed15dc88d49f4994c30ecacd6`)
  — real-handler envelopes, direct-address limiting, expiry/success recovery,
  capacity/LRU and concurrent limiter coverage.

Workstream B owns two Tunnel files:

- `internal/tunnel/server.go`
  (`663a037312bf7e675a430569d24fb3569251e9f8fa913eb9e388c9d49f4b0741`)
  — bounded source-attempt state, synchronous expiry and deterministic LRU.
- `internal/tunnel/source_limiter_test.go`
  (`19b68e45d467d46749a887e4bc0ab5f16441831777f5f1f4310e093d16b6e6d4`)
  — hard-cap, expiry, clock rollback, attempt/success and concurrent coverage.

No other implementation or test file was changed by revision 1.

## Behavior Changed

### Login KDF admission

- `auth.Service.Login` checks cancellation before work and again immediately
  before password verification. A package-level buffered admission channel is
  shared by every Service instance and permits exactly two concurrent password
  verifications process-wide.
- A third verification fails immediately with the typed
  `auth.ErrVerificationBusy` without entering Argon2. The slot is released by
  defer on success, verification error or panic. Cancellation before
  verification consumes neither KDF work nor a slot.
- Non-busy verifier failures are reduced to the safe typed
  `auth.ErrVerificationFailed`; the raw verifier/hash parse error is not
  propagated into HTTP logging. Store lookup, random Session creation and
  digest-only persistence remain unchanged.
- `httpapi` depends only on the three Auth methods it uses, so a deterministic
  fake can drive the actual handler. Production still supplies
  `*auth.Service`. Busy returns the stable request-linked JSON
  `503 SERVICE_UNAVAILABLE` envelope and is not recorded as a credential
  failure.

### Bounded browser failure state

- Login has five failures per one-minute window keyed only by the host parsed
  from the direct `Request.RemoteAddr`; forwarded headers are ignored.
  Invalid JSON and invalid credentials count. The sixth request receives
  `429 RATE_LIMITED`; successful login deletes the source entry.
- Login and pairing-claim each use the same mutex-protected limiter
  implementation. Each map has an exact 4096-entry hard cap and one-minute
  fixed-window/idle reclamation. A new key first synchronously reclaims expired
  entries and then evicts the least-recently-observed entry if still full.
  Cleanup is bounded by the cap and creates no goroutine.
- Existing Pairing response/status and success-delete semantics are retained.

### Bounded Tunnel source state

- Tunnel keeps its existing twenty-attempt/one-minute default and the
  `Gateway.Now` clock. The source map now has an exact 4096-entry hard cap.
- Only a new source can grow the map. Before insertion it synchronously
  reclaims expired fixed windows and, if still full, evicts exactly one
  least-recently-observed source. Timestamp plus monotonic observation order
  provides deterministic ties and fails closed across wall-clock rollback.
- Current-source attempt rejection, expired-window reset and successful-auth
  deletion remain unchanged. The limiter creates no cleanup goroutine.

## Focused Workstream Evidence

The following evidence comes from the two revision-coder handoffs in their
separate ASD-Host staging trees. The integration owner did not silently
reclassify or rerun these as different commands.

### Workstream A

- First `auth`/`httpapi` count-one run: Auth **PASS** in 0.171s; HTTP **FAIL**
  only because the test inserted `replacement` at `start+2s` but expected it
  expired at `start+60s`, when its age was only 58 seconds. Production
  correctly retained it. The oracle was mechanically moved to `start+62s`.
- Identical count-one retry: Auth **PASS** in 0.176s and HTTP **PASS** in
  2.695s. MemAvailable was 5,466,340 -> 5,406,920 kB; SwapFree remained
  2,058,556 kB.
- Six exact admission/limiter regressions with `-count=10`: Auth **PASS** in
  0.004s and HTTP **PASS** in 34.468s. MemAvailable was
  5,474,752 -> 5,447,688 kB; SwapFree remained 2,058,556 kB.
- Full affected-package race: Auth **PASS** in 1.380s and HTTP **PASS** in
  38.678s. MemAvailable was 5,454,188 -> 5,355,072 kB; SwapFree remained
  2,058,556 kB.

### Workstream B

- First Tunnel count-one run: **FAIL** only because the test's first attempt
  was at `start+1s` but expiry was asserted at `start+60s`, an age of 59
  seconds. The attempt loop was mechanically corrected to start at offset
  zero.
- Identical Tunnel count-one retry: **PASS** in 1.700s.
- Exact `^TestSourceLimiter` regressions with `-count=20`: **PASS** in
  13.972s. MemAvailable was 5,465,096 -> 5,459,664 kB; SwapFree remained
  2,058,556 kB.
- Full Tunnel race: **PASS** in 7.785s. MemAvailable was
  5,483,264 -> 5,451,460 kB; SwapFree remained 2,058,556 kB.

Both focused staging trees were deleted after their file hashes matched the
working tree. Neither first failure was a production assertion or hidden test
failure; each was a deterministic test-time boundary error and is retained
here verbatim.

## Final Merged-Tree Verification

The integration owner created fresh private staging
`/tmp/aisummoner-task009-r1-final.81gWJn`. It excluded `.git`, real `.env`,
data, Web `node_modules`/`dist`, coverage and binaries. It explicitly retained
and checked `.env.example`, `.dockerignore`, README, `opencode.json`, the
`.opencode` tool, deployment files, migrations, the static placeholder and Web
lock/source files.

- The staged tree contained 187 files and 84 Go files. Remote `gofmt` over all
  Go files produced no retained difference. The local and remote sorted
  187-file SHA-256 manifests matched line-for-line at
  `4814e5e25207ab22c945fc3d6a7b4c4915b1b24e0b041efc10b7888f6988b1f3`.
  `git diff --check` passed.
- `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 600s ./...` — **PASS** all 24
  targets (23 test packages plus migrations with no tests), wall 17s.
  MemAvailable was 5,455,280 -> 5,348,684 kB; SwapFree remained
  2,058,556 kB.
- `GOMAXPROCS=2 go test -race -count=1 -p 1 -timeout 900s ./internal/...`
  — **PASS** all 21 internal packages, wall 106s. The revised HTTP package
  passed in 39.026s and Tunnel in 9.488s. MemAvailable was
  5,452,872 -> 5,414,580 kB; SwapFree was 2,058,556 -> 2,057,788 kB.
- `GOMAXPROCS=2 go vet ./...` — **PASS** with no output, wall 1s.
  MemAvailable was 5,478,928 -> 5,477,276 kB; SwapFree remained
  2,057,788 kB.
- `GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server
  ./cmd/aisummoner-client` — **PASS** with no output, wall 26s.
  MemAvailable was 5,478,688 -> 5,358,408 kB; SwapFree remained
  2,057,788 kB.

Every step ran only after a >=3 GiB memory and no-competing-Go gate, ran
sequentially, and left no Go tool process. The exact final staging tree and its
temporary manifest were deleted after verification.

## Retained Revision 1 Deviations And Residuals

- No implementation deviation from Task009 revision-1 scope is known. The
  changed-file count is seven: five Workstream-A files and two Workstream-B
  files.
- Task008's production Server Docker image is still not claimed: ASD-Host
  timed out reaching `proxy.golang.org` during `go mod download`. Task010 must
  retry from a network-capable environment or deploy the directly verified
  binaries before acceptance.
- Live browser/Caddy TLS, two-cloud-host reconnect/offline behavior, dynamic
  pairing-output permissions and real OpenCode availability remain Task010
  acceptance work. They do not replace the required independent Task009
  revision-1 review.

## Revision 2 Deviations And Residuals

- No production deviation from plan revision 2 is known. Revision 2 changed
  sixteen files across the two authorized disjoint workstreams; the later two
  Task010 planner documents are durable integration context, not Task009
  implementation.
- The operational/test-evidence anomalies are fully disclosed above: missing
  `rsync`, one Header canonicalization fixture error, missing `/usr/bin/time`,
  one locale-order aggregate mismatch with an empty file diff, and two client
  output-channel losses whose results were not accepted. Every affected gate
  was rerun mechanically with explicit complete evidence.
- Task008's Server Docker image remains unclaimed because ASD timed out at
  `proxy.golang.org`. Task010 must retry from a network-capable environment or
  deploy the directly verified binaries. Task009 did not build an image.
- Live browser/Caddy TLS, two-cloud-host reconnect/offline behavior, dynamic
  pairing-output permissions and real OpenCode availability remain Task010
  acceptance work. Task010 stays blocked until independent Task009 revision-2
  approval.

## Revision 2 Reviewer Handoff

Independently verify the exact trusted-proxy boundary, Caddy overwrite and
private topology, one shared Resolver construction, resolve-before-limiter
ordering, A/B source isolation, untrusted header anti-spoofing, malformed
trusted request zero-mutation behavior and source/header log redaction. Also
confirm revision 1's KDF gate and bounded HTTP/Tunnel limiter semantics remain
unchanged. The reviewer must issue a fresh decision before Task010 starts.

## Retained Revision 1 Reviewer Handoff

Independently recheck only the two revision-0 required fixes and their
regressions, including process-global KDF admission, safe 503 behavior,
direct-address login state, both bounded HTTP maps, Tunnel capacity/expiry/LRU
and race behavior. Task010 remains blocked until that review is `APPROVED`.
