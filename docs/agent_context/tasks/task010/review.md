---
task_id: task010
type: review
status: approved
from: task010_acceptance_reviewer
to: orchestrator
revision: 2
decision: APPROVED
next_action: finish
---

# Task 010 Acceptance Review — Revision 2

## Decision

APPROVED

No blocking issues found. The user-authorized TCP 10001 topology removes the
sole external condition behind revision 0's `BLOCKED` decision. The fresh
three-host deployment now supplies a directly reachable HTTPS/WSS endpoint,
strict production Client trust, Browser Terminal and Agent evidence, while
preserving every reviewed internal and host boundary.

## Findings

1. **The revision-0 blocker is superseded, not concealed.** Revision 0 found
   that ASD intentionally did not admit inbound TCP 443, so its scoped SSH
   forward could prove application TLS but not the then-planned outside-in
   endpoint. The human operator subsequently authorized one already-open,
   unused ASD port from a bounded set. Identity-checked probes showed 10001 and
   10002 reachable and unused; revision 2 selected only 10001 and left 10002,
   443, 80/nginx and the remaining candidate ports untouched. The reviewer
   independently reached `122.51.70.33:10001` from both local and lzr. This is
   the exact external boundary that revision 0 could not prove.

2. **The test CA is acceptable for the authorized MVP test deployment and is
   not presented as public PKI.** The baseline requires public traffic to use
   HTTPS/WSS and the Client to verify Server identity through TLS; it does not
   require an ACME/public root for this three-day functional Demo. Task010
   already allowed a scoped test CA when issuance was unavailable, and
   revision 2 explicitly authorizes that mechanism because TLS-ALPN cannot run
   on the selected non-443 port. The leaf certificate names
   `122-51-70-33.sslip.io`, has a two-day lifetime and server-appropriate key
   usage; the CA has certificate-signing usage. System trust correctly rejects
   it, while the private lzr CA file verifies it successfully. The Client uses
   `SSL_CERT_FILE`, no `--dev`, no root override and no insecure-skip-verify
   path. Documentation consistently calls this a short-lived MVP test
   deployment rather than a publicly trusted production site.

3. **The external and internal runtime boundaries match the frozen record.**
   At reviewer sampling, lzr Client PID `595619` ran as uid 1003 from the fresh
   run path; its live executable hash was the recorded
   `03bab20d...2d747`, it owned no listener, and its established socket went
   directly to `122.51.70.33:10001`. Strict lzr health returned HTTP 200 with
   certificate verification result zero and TLS 1.3 `Verification: OK`.
   Without the scoped CA, system trust rejected the certificate as expected.
   From lzr, 8088, 10002, 14096 and 14097 were unreachable.

   ASD Server PID `1594510` ran as uid 1001 from the fresh run path with live
   hash `d9e4bf8f...b6bd06d`, listening only on `127.0.0.1:8088` in Fake mode.
   The exact Caddy container ID and image matched the record; it ran as
   `1001:1001`, host-networked, read-only, with `restart=no`, and Caddy alone
   listened on `*:10001`. Its mounts include only the leaf chain/key and Caddy
   state/config paths—not the CA private key. The Caddyfile overwrites
   `X-AISummoner-Client-IP` with `{remote_host}` before proxying to loopback,
   preserving Task009's exact trusted-peer provenance.

4. **The Browser evidence has adequate, falsifiable oracles.** The frozen
   Playwright package remains exactly version 1.60.0, one worker, zero retries,
   and retains no screenshot, video or trace. `reclaim` performs real login,
   fresh-code claim, same-code rejection, Online/metadata assertions and a
   real lzr Terminal command. `tls-smoke` asserts HTTPS login plus
   Secure/HttpOnly/SameSite=Strict cookie attributes; observes the Terminal
   WebSocket; proves lzr rather than ASD/local using hostname, uname and uid;
   exercises PTY interaction and compares the actual resize frame with remote
   `stty size`; then approves two distinct Fake `remote_exec` calls over SSE
   and requires exit 0, non-truncation and lzr/Linux assistant output. Their
   recorded 2.5-second and 3.5-second PASS results therefore cover the public
   HTTPS, WSS and SSE rerun rather than a health-only smoke.

5. **Host preservation and evidence hygiene remain intact.** nginx is active
   and its six configuration hashes remain byte-identical to the preflight
   baseline. The four unrelated ASD containers are still present with their
   original images/ports. Runtime stayed under the documented resource gates;
   current Client RSS remained about 17 MiB and Server RSS about 24 MiB. The
   repository contains no `.env`, certificate/key, database, log, screenshot,
   trace or video artifact. `web/e2e/.playwright-output` contains only the
   45-byte non-secret `passed` marker. Revision 2's log-redaction scan and
   pairing-output truncation evidence are recorded without secret values.

## Baseline-0 And Task010 Acceptance

All twelve Baseline-0 items retain the independently accepted revision-0
functional/lifecycle evidence. Revision 2 freshly re-proves the portions that
changed with the public topology:

- fresh identity pairing, same-code rejection and Online metadata: **PASS**;
- production Client direct WSS with strict scoped CA trust and no listener:
  **PASS**;
- Browser direct HTTPS login and secure cookie attributes: **PASS**;
- Terminal WSS, interactive PTY, remote hostname/uid and actual resize:
  **PASS**;
- Agent SSE, two per-command approvals, tool output/exit and final answer:
  **PASS**;
- outside-in health from both local and lzr, with internal/fallback ports
  unavailable externally: **PASS**.

The unchanged Task010 criteria also remain satisfied by the hash-frozen
revision-0 evidence: clean Go test/vet/build and Web test/build; full Fake
lifecycle; Offline/reconnect/newest-wins/restart/unpair/reclaim; cross-owner
integration boundaries; real OpenCode classified `available`; resource gates;
redaction; and scoped recovery instructions.

## Non-Blocking Residuals

- The certificate is a short-lived private test-CA certificate. Browsers need
  explicitly scoped test trust, and the deployment must not be described or
  used as a generally trusted production website. Replace it with a normal
  public certificate before non-test use.
- Runtime Agent revocation tombstones grow until process restart.
- A bounded post-commit cleanup timeout may leave an uncooperative worker
  finishing after logical authorization is already revoked.
- Vite reports an approximately 557-KiB JavaScript chunk.
- The Caddy CA/leaf private keys and other revision-2 secret-bearing controller
  files remain intentionally present only for the running review snapshot;
  the documented exact cleanup must remove them after handoff. Caddy itself
  cannot access the CA private key.

## Reviewer Verification

- Read completely: Codgent reviewer workflow/template, repository
  `AGENTS.md`, all four baselines, ADR-0001/0002, durable project context,
  Task010 plan revision 2, summary revision 2, acceptance/cleanup records,
  revision-0 review history and the relevant frozen Playwright source.
- Frozen document hashes matched exactly: plan
  `a6d9b286cbbc3ed5ea41f631158d7829b2d6750e7c2ce19b2a74b075f4e0f984`,
  summary
  `001de413da086cead21ea9fd08446ca4b74aa2d0a3b93b378f8364a22899d648`,
  acceptance
  `1bd70c67fc956f485af5285f674945e721a30b9f8ebd70c42eb189b8e2e50cb7`,
  README
  `d0dcae185569c00f7062706b968465c64a3977ff4646f5cb5c19cc940ade3828`,
  state
  `e4ab9393902a5366d3ba6ede0e9577cb38defcbfdf32a9b14cc09c138c3e15b2`
  and todo
  `cf46008515ea02cdd046a214f88648fc0028dcf2bea2703cc6dd6818ff4b0774`.
  E2E lock/spec remain `e40bcca7...d5feb` / `aa9c210c...71903`.
- Bounded read-only lzr probes verified exact Client PID/uid/path/hash/argv,
  zero Client listeners, direct established public connection, scoped public
  CA metadata, strict TLS health, system-trust negative control and external
  negative ports.
- Bounded read-only ASD probes verified exact Server PID/uid/path/hash,
  listener ownership, exact Caddy container/image/user/network/read-only/
  restart/mount metadata, Caddy proxy structure, public certificate metadata,
  nginx state/hashes and preservation of unrelated containers.
- Local direct public health and login-page probes both returned HTTP 200 from
  `122.51.70.33:10001`. Repository and Playwright-output filename/metadata
  inspection found no sensitive runtime artifact. `git diff --check` passed.
- No env values, credentials, private-key contents, database contents or log
  contents were read. Heavy Go/race/Node/Browser tests were not repeated; the
  review used frozen PASS evidence, source oracle inspection and live bounded
  falsification.

## Revision 0 History

Revision 0's `BLOCKED` decision was correct for its frozen topology: direct
TCP 443 remained unreachable and scoped forwarding bypassed that missing
boundary. It requested no implementation changes. Revision 2 preserves that
history in the acceptance/cleanup records while replacing only the deployment
listener under explicit user authorization. Direct `10001` strict-TLS Client,
Browser HTTPS/WSS/SSE and negative-exposure evidence now closes that exact
blocker.

## Next Action

- Task010 is accepted; finish MVP-0 and proceed with the exact revision-2
  cleanup/handoff recorded in the acceptance document.
- Before cleanup, revalidate the exact Client PID/start/path/uid, Server
  PID/start/path/uid, and Caddy container ID. Stop only those three scoped
  components, remove revision-2 runtime/browser credentials and CA/leaf
  private keys, and leave nginx, unrelated containers and revision-0 audit
  state untouched. Retain SQLite/Device identity only according to the stated
  recovery choice.
