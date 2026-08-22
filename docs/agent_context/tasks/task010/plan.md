---
task_id: task010
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 2
requires_review: true
---

# Task 010 Plan: Clean Build, Controlled Deployment And Three-Host Acceptance

## Revision 2 Public-Port Amendment

The human operator clarified on 2026-08-13 that ASD intentionally does not
publish TCP 443 and authorized use of an already-open, otherwise unused port
from `8080`, `10000`, `10001`, or `10002`, provided no running service is
disturbed. The main agent performed bounded sentinel probes without touching
nginx or existing containers:

- TCP 80 is owned by the existing nginx and remains out of scope.
- TCP 8080 and 10000 were free locally but timed out from lzr while a verified
  listener was present.
- TCP 10001 and 10002 were free locally and returned the exact probe sentinel
  from lzr; every probe process was identity-checked, stopped, joined, and its
  listener removed.

Revision 2 therefore replaces only the public listener requirement:

- use `https://122-51-70-33.sslip.io:10001` as the dedicated public endpoint;
- keep 10002 unused as a fallback and do not bind or mutate ports 80, 8080,
  10000, or 443;
- use a fresh scoped test CA because public ACME TLS-ALPN issuance requires
  port 443; install only the public CA on lzr through a private `SSL_CERT_FILE`
  and run the production Client without `--dev` or insecure TLS flags;
- keep Server on `127.0.0.1:8088`, OpenCode on `127.0.0.1:14096`, Bridge on
  `127.0.0.1:14097`, and the exact trusted-proxy/header-overwrite boundary;
- rerun direct lzr public Client WSS plus Browser HTTPS/Secure-cookie/Terminal
  WSS/Agent SSE, then return this same task for independent revision-2 review.

This is a deployment-topology correction authorized by the user, not a product
security downgrade or an implementation change.

## Revision 1 Execution Workstreams

Task009 revision 2 is approved. Task010 now runs three coordinated workstreams:

1. The main integration agent exclusively owns remote mutations, artifact
   transfer, Server/Client processes, SSH forwarding, pairing, secrets, TLS,
   OpenCode and final cleanup. No subagent may start/stop a remote process or
   create remote files without an explicit per-step handoff.
2. A browser-harness coder owns only a new lock-versioned browser acceptance
   harness and its local documentation/tests. It may read Web source/selectors
   and run local/isolated Node commands after a memory gate, but receives no
   deployment secret and performs no remote mutation.
3. A deployment safety observer performs read-only local/ASD/lzr inventory and
   cross-checks scoped paths, ports, exact peer/TLS and rollback steps. It must
   not mutate services, files, containers, firewall or nginx.

The independent acceptance reviewer remains separate until the evidence
record is frozen. Browser and deployment workstreams report checkpoints to the
main agent; only the main agent writes the acceptance record and Task010
summary. All secret-bearing runtime files stay outside the repository.

## Status

Ready only after task009 approval. This task is authorized to deploy AISummoner-scoped files/processes to the named test hosts, but must preserve unrelated services and record cleanup/recovery.

## Owner

Main Integration/Test Agent, with bounded delegated probes where safe.

## Topology

```text
Local workstation: Browser/Playwright + optional tunneled real OpenCode smoke
ASD-Host (122.51.70.33): AISummoner Server + SQLite + TLS proxy + OpenCode/fake runtime
lzr-host (101.43.48.120): Linux Remote Client only
```

- SSH aliases/keys are already configured.
- ASD currently has nginx on port 80 and public 443 available; lzr has existing services and low free memory.
- A resolvable test hostname such as `122-51-70-33.sslip.io` points to ASD. Confirm immediately before use.
- Do not stop, reconfigure or replace unrelated nginx/Docker/services. Bind AISummoner Server to `127.0.0.1:8088`; use a dedicated TLS listener/proxy on currently free 443 only after a fresh check.

## Goal

Reproduce all builds/tests from a clean source snapshot, deploy only the two AISummoner binaries/assets/configs, run an automated browser-to-Remote full-chain smoke plus manual lifecycle checks, attempt a real OpenCode free-model turn, and create an evidence-backed acceptance record for all 12 MVP checks.

## Resource Gates

- Local: before Node/Chromium/Docker require `MemAvailable >= 8 GiB`, `SwapFree >= 4 GiB`; one heavy job at a time; Node heap 2048 MiB; Playwright workers=1.
- ASD: before Go/OpenCode/image work require `MemAvailable >= 3 GiB`; pause new work below 2 GiB; `GOMAXPROCS=2`, `-p 2`.
- lzr: require `MemAvailable >= 1.2 GiB` to start tests; below 1 GiB stop AISummoner extras; never build Node/OpenCode/Docker there; Remote RSS target below 150 MiB.
- Capture memory before/after each heavy phase. Do not use long blocking waits without progress updates.

## Clean Build And Deterministic Tests

1. Preserve the working tree, make an isolated source archive/staging directory excluding `.git`, secrets, data, node_modules and build outputs.
2. On ASD isolated staging run sequentially:
   - `GOMAXPROCS=2 go test -count=1 -p 2 ./...`
   - `GOMAXPROCS=2 go vet ./...`
   - `GOMAXPROCS=2 go build -trimpath -p 2` for Server and Linux amd64 Client.
3. Locally run `npm ci`, Vitest and production build with the memory cap.
4. Validate Compose and build the deployment image if not freshly proven by task009.
5. Run Playwright local/in-process fake fixtures first, single worker, before touching remote deployment.

## Controlled ASD Deployment

- Resolve exact existing process/port/container/service state before mutations.
- Create a dedicated versioned AISummoner directory and private data/config directories, mode 0700, owned by the chosen unprivileged service user. Secrets are freshly generated in a 0600 env file and never printed or copied back into repository/docs.
- Bootstrap one test admin; after successful DB bootstrap, remove/unset the plaintext bootstrap password and prove restart succeeds without it.
- Run Server as non-root on `127.0.0.1:8088`. Use the reviewed service/Compose asset with bounded restart/shutdown.
- Provide HTTPS/WSS on a dedicated public hostname/443 with Caddy or isolated proxy config. Prefer a publicly trusted certificate via TLS-ALPN on free 443. If external issuance is unavailable, use a dedicated test CA, install only its public CA on lzr, and configure Playwright to trust/ignore only this scoped test endpoint; never use client insecure-skip-verify in production mode.
- The direct loopback/Fake phase keeps `AISUMMONER_TRUSTED_PROXY_IPS` empty.
  The isolated TLS Caddy must use host networking (or an equivalently proven
  exact peer), overwrite `X-AISummoner-Client-IP` with `{remote_host}`, and the
  restarted Server must set `AISUMMONER_TRUSTED_PROXY_IPS=127.0.0.1`. Do not
  trust a subnet/CIDR or generic forwarded header. Before acceptance, prove a
  forged dedicated/XFF header on a direct untrusted test request cannot change
  its key, while two client source IPs represented by the trusted proxy retain
  independent Login and Tunnel windows.
- Verify public health, login, WebSocket upgrade and SSE response. Internal OpenCode bridge and sidecar port must not be reachable from public interfaces.
- Keep a rollback note and avoid modifying nginx port80 configuration.

## Controlled lzr Deployment

- Copy only the verified Linux amd64 Client binary into a dedicated user-owned directory; preserve unrelated processes/files.
- Start as `myself`, never root, with a fresh private identity directory and production HTTPS Server URL/trusted CA.
- Capture stdout pairing code through a private mode-0600 log or pipe and redact/delete it after claim. Structured logs must not contain the code/private key.
- Record Client RSS, identity modes, outbound connection, no listening TCP port, and automatic reconnect behavior.
- Use a scoped systemd-user/transient/background process whose PID is verified before stop/restart.

## Automated Three-Host E2E (Fake Adapter)

From local Playwright or an equivalent browser-level single-worker test:

1. Login over HTTPS.
2. Claim the fresh lzr pairing code; submit it again and assert one-use failure.
3. Assert Device metadata and Online.
4. Open xterm; run unique commands that prove execution location (`hostname`, `uname -a`, `id -u`, `printf` stdout/stderr/exit), and assert hostname is lzr rather than ASD/local.
5. Resize browser terminal and verify remote `stty size` matches sent rows/cols; run one simple interactive PTY behavior.
6. Create default per-command Agent Session; prompt for system information; assert `remote_exec` pending card, approve once, started/output/exit/final answer, with remote hostname lzr.
7. Create a second per-command Session; use approve-session and prove its next command skips a prompt while a different Session still asks. Exercise deny.
8. Stop only the lzr Client; within 15 seconds assert Offline and Terminal/Agent failure/closure. Restart with the same identity and assert automatic/restarted reconnect Online without re-pairing.
9. Unpair and assert the live Tunnel/Terminal/Agent close immediately; Remote later receives a new code and old ownership no longer authorizes.
10. Test unauthenticated and unknown/non-owned Device/Session/Tool paths return standard errors. Cross-owner behavior may use the approved in-process integration test because MVP deployment intentionally has one admin.

The automated smoke must fail if commands execute on ASD or local.

## Real OpenCode Smoke

- First try ASD's reviewed OpenCode mode without adding a paid API credential. Confirm sidecar health/version/model configuration and the two tool-deny layers.
- If ASD legitimately lacks the already-configured free authentication, do not copy personal credentials silently. A permitted fallback for test only is to keep the authenticated local OpenCode sidecar on loopback and expose it to ASD solely through an SSH local/reverse loopback tunnel; likewise expose only ASD's loopback bridge back to that local tool. Record this as test topology, not deployment topology.
- Send one prompt requiring `remote_exec hostname`, approve it and assert the tool result is lzr. Report exactly:
  - `available`: real text + remote tool loop completed;
  - `rate_limited`: provider returned an identifiable 429/rate-limit state;
  - `unavailable`: auth/network/version failure.
- Never substitute a Fake response and call it real. External rate limit does not invalidate deterministic E2E but remains a disclosed external acceptance constraint.

## Additional Lifecycle/Security Checks

- Root Client refusal (without dev flags).
- Device private key 0600 and stable Device ID after restart.
- Server restart without bootstrap password and with persisted Device/Agent history as designed.
- Pairing expiry/invalid, exact Origin rejection, cookie flags, output truncation/timeout, Agent denial.
- Tunnel heartbeat/newest-wins via a second scoped Client instance with the same identity.
- No AISummoner Client listening ports on lzr; no bridge/OpenCode public port on ASD.
- Trusted-proxy provenance is exact: Caddy overwrites the dedicated source
  header, Server trusts only the resolved immediate Caddy peer, malformed or
  missing trusted headers fail before limiter state, and different public
  sources cannot globally lock Login or Tunnel reconnect.
- Inspect scoped logs for password, cookie, pairing secret/code after claim, bridge/basic auth, private key, sentinel terminal input, Agent command/output sentinels.
- Graceful shutdown leaves no AISummoner child process/listener/temporary secret on each host.

## Acceptance Record

Write `docs/acceptance/mvp-0-2026-08-13.md` containing:

- build/test command and pass/fail evidence;
- versions/host roles and non-secret endpoint;
- a table for all 12 baseline acceptance items with evidence;
- Fake Adapter E2E result;
- real OpenCode classification and exact external blocker if any;
- memory/RSS observations;
- security/lifecycle checks;
- remaining known issues split into MVP blocker versus Alpha follow-up;
- deployed AISummoner paths/services and safe stop/remove instructions (do not include secret values).

Update task010 summary and README operational status. Do not commit logs, DB, screenshots containing codes, environment files or credentials.

## Review And Cleanup

- An independent agent reviews the acceptance record against raw non-secret command evidence and running endpoint behavior.
- Leave the demo running only if it is TLS-protected, unprivileged, uses fresh secrets, exposes no internal ports, and does not interfere with existing services. Otherwise stop AISummoner processes and state whether scoped test data remains/recovery path.
- Remove isolated `/tmp` staging and pairing-code logs. Do not delete user data outside explicitly resolved AISummoner paths.

## Acceptance Criteria

Task010 is ready for approval when:

- clean deterministic test/build matrix passes;
- browser → ASD Server → lzr Remote full chain passes Terminal and Fake Agent approval;
- Online/Offline/reconnect/unpair semantics pass;
- all 12 baseline items have honest evidence;
- real OpenCode is honestly classified, with remote hostname proof if available;
- resources stayed above gates and deployment did not disturb unrelated services;
- acceptance/cleanup documentation is complete and independently reviewed.
