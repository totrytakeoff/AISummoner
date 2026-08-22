---
task_id: task010
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 2
review_required: true
---

# Task 010 Summary

## Revision 2 Public-Port Acceptance

The human operator clarified that ASD intentionally does not expose TCP 443
and authorized an already-open, unused port without disturbing existing
services. Exact bounded probes showed 10001 and 10002 free and reachable from
lzr; 10001 was selected, 10002 remained unused, and all probes were joined.

Fresh run `20260813T072250Z` used a new database, secrets, test CA and Device
identity. The reviewed Server/Client binaries were reused by exact hash. Server
ran as uid 1001 on `127.0.0.1:8088`; Caddy ran as uid 1001 with host network,
read-only filesystem and `restart=no`, exposing only
`https://122-51-70-33.sslip.io:10001`; Client ran as uid 1003 directly over
strict TLS with a private CA file, no `--dev`, and no listener.

Playwright 1.60.0 passed the public `reclaim` test in 2.5 seconds and the full
HTTPS/Secure-cookie/Terminal-WSS/Agent-SSE `tls-smoke` in 3.5 seconds. lzr
could not reach 8088/10002/14096/14097. nginx remained active with all six
hashes unchanged and all four unrelated containers present. Eleven log-secret
categories were absent across 5,914 bytes; pairing output was 0600 and size 0.
Resources remained above gates. The former external-443 blocker is superseded;
revision 2 is ready for independent runtime review.

Intermediate failures were fail-stopped and retained: root-owned parent denied
the first new-run mkdir before any file existed; the first Caddy config had a
one-line global-options syntax error and exited without binding; the first
unused new database bootstrap lacked a retained browser credential and was
exactly reset; one anchored Playwright grep found no test body. Each correction
was narrow, and the final public tests passed.

The revision-2 demo was left running through review: Server PID 1594510, Client
PID 595619 and exact Caddy container ID
`57646701ad0838148454451c86be533b461e5fd8446b6b66d19d50466a212008`.
After the independent `APPROVED` decision, all three were identity-checked,
stopped and joined/removed; new secrets/private keys/local E2E material were
deleted without touching revision-0 audit state, nginx or unrelated containers.
SQLite, Device identity, binaries and non-secret logs were retained for audit.

## Revision 0 Historical Summary

The sections below preserve the original revision-0 implementation, full
lifecycle/OpenCode evidence, public-443 blocker and cleanup handoff. They are
historical where they conflict with revision 2 above.

## Files Changed

- `internal/httpapi/api.go`, `api_test.go` — made state-changing Browser APIs
  require exactly one Origin value and added allowed-first/double-allowed
  duplicate-Origin regressions.
- `internal/agentapi/api.go`, `api_test.go` — applied the same exact-Origin
  boundary to Agent mutations.
- `web/e2e/` — added a separately locked Playwright 1.60.0 package, safe
  one-worker/no-artifact configuration, bounded environment/checkpoint/API/
  Agent/Terminal helpers, full Fake lifecycle/reclaim phases, scoped TLS smoke
  and real OpenCode smoke.
- `web/vite.config.ts` — restricted Vitest discovery to production Web unit
  tests so the independent Playwright package is not executed by Vitest.
- `.gitignore`, `README.md` — excluded E2E runtime output and documented the
  verified functional state plus the remaining public-ingress blocker.
- `docs/acceptance/mvp-0-2026-08-13.md` — froze the 12-item acceptance matrix,
  hashes, versions, security/lifecycle evidence, OpenCode classification,
  resource observations, blocker and exact cleanup boundary.
- `docs/agent_context/tasks/task010/{summary.md,review.md}`,
  `docs/agent_context/{state.json,todo.md}` — workflow handoff files; the coder
  did not write a review decision.

## Deployment And Acceptance

- Run ID `20260813T024159Z` used only explicit 0700/0600 paths under
  `/home/myself/.local/{opt,state}/aisummoner-task010/` on ASD/lzr and a local
  private state directory. ASD/lzr services ran as uid 1001/1003, never root.
- A clean isolated snapshot was tested on ASD, production Web assets were built
  locally and embedded, and the Linux/amd64 Client hash matched on lzr. Server
  remained loopback `127.0.0.1:8088`; OpenCode/Bridge remained loopback
  `14096/14097`; Caddy alone used 443. Existing nginx and four unrelated Docker
  workloads were preserved.
- The full Fake browser flow passed login, one-use pairing, Device metadata,
  real Terminal PTY/interactive/resize, two-tool approval, approve-session,
  deny, Offline/tool failure, same-identity reconnect, authenticated
  newest-wins, Server restart without bootstrap password, unpair invalidation
  and fresh-code reclaim.
- A scoped test CA proved strict Client TLS plus Browser HTTPS/WSS/SSE. Trusted
  proxy tests proved exact peer/header overwrite, two-source Login/Tunnel
  isolation, forged-header immunity and malformed fail-before-limiter.
- Real OpenCode is classified **available**: pinned 1.18.11, canonical
  deny-by-default policy/tool, shared private workspace, external Session,
  exactly one approved `remote_exec`, exit 0, non-empty lzr hostname/Linux
  evidence and assistant final text. No Fake fallback was used.
- Public 443 remains an external deployment blocker. Caddy listened and host
  UFW allowed 443, but local/lzr outside-in traffic and TLS-ALPN issuance timed
  out at the cloud/upstream ingress boundary. The acceptance record does not
  claim a public demo.

## Verification

- `GOMAXPROCS=2 go test -count=1 -p 2 ./...` on fresh placeholder staging:
  PASS, 25 targets. `GOMAXPROCS=2 go vet ./...`: PASS.
- Duplicate-Origin focused packages: PASS 0.308/0.570 s; count 20 PASS
  2.188/4.295 s; race PASS 38.493/9.270 s. Raw live duplicate requests returned
  403 `ORIGIN_FORBIDDEN` after deployment.
- `npm --prefix web test -- --run`: PASS, 8 files/22 tests.
  `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build`: PASS,
  two hashed assets, no placeholder.
- E2E `tsc --noEmit` and exact list gates: PASS. `fake-lifecycle` and `reclaim`:
  PASS. Scoped TLS smoke: 1 PASS in 3.9 s. Real OpenCode smoke: 1 PASS after a
  timeout-only harness correction.
- Dynamic root refusal, real pairing expiry (410 after the ten-minute expiry),
  exact Origin, Secure/HttpOnly/Strict cookie, no Client listener, private
  modes, restart/newest-wins/unpair, and six unchanged nginx hashes: PASS.
- Redaction scan: 393,749 scoped log bytes, 19 non-empty secret/sentinel
  categories, every `present=false`; all pairing-output files mode 0600/size 0.
- Resources remained above every plan gate. At freeze: Server RSS 79,104 KiB,
  Client RSS 12,040 KiB, OpenCode about 636 MiB, Caddy about 18 MiB.

Intermediate failures retained in the acceptance record: one full Go staging
used generated Web assets and therefore tripped only placeholder-clean-checkout
oracles; the first Server restart command referenced a wrong env path and
stopped before replacement; the first test CA lacked strict CA key usages; an
anchored Playwright grep found zero tests; the first real provider Session
produced its pending tool just beyond the original 30-second oracle. Each was
fail-stopped, diagnosed without leaking values, corrected narrowly and rerun.

## Cleanup / Running State

- Review handoff intentionally leaves the scoped TLS/OpenCode demo running so
  the independent reviewer can inspect non-secret runtime boundaries: exact
  Server PID 1530187, Client PID 524331, Task010-named Caddy/OpenCode containers
  with `restart=no`, and private ASD/lzr SSH control sockets.
- Running does not mean publicly reachable: access is only through the scoped
  local test-CA forward while cloud 443 ingress is blocked.
- After review, cleanup order is exact Client TERM/join → SSH controls exit →
  exact Server TERM/join → stop/remove only the two Task010-named containers.
  Retain SQLite/state for recovery; remove pairing/env/certificate private-key
  browser copies. Never use broad `pkill`, `compose down`, shared network
  deletion or nginx mutation.

## Known Issues

- **MVP deployment blocker:** public ASD 443 is blocked at cloud/upstream
  ingress. Open it and rerun public certificate, Client WSS, Browser HTTPS/WSS/
  SSE, cookie and external exposure checks.
- Runtime revocation tombstones intentionally grow for one process lifetime.
- A bounded post-commit timeout can leave an uncooperative cleanup worker
  completing in the background after logical authorization is removed.
- Vite reports an approximately 557-KiB JS chunk; functional build passed.
