---
task_id: task010
type: preflight
status: ready_after_task009
from: task008-lifecycle-coder
to: task010-coder
revision: 0
---

# Task 010 Three-Host Acceptance Preflight

## Gate And Scope

Do not start this plan until Task008 and Task009 are approved. Task010 may then
deploy only versioned AISummoner-scoped artifacts and processes. It must not
stop, replace or reconfigure unrelated nginx, Docker workloads or host
services.

The probe facts below are stale planning evidence and must be refreshed before
each phase:

- local historically had 11--15 GiB available memory and about 9.3 GiB free
  swap; it has the authenticated OpenCode 1.18.11 installation but has also
  observed external rate limiting.
- ASD historically had about 5.2--5.5 GiB available memory, 2 GiB free swap and
  Go 1.24.4. Its previously observed OpenCode was 1.17.8 without confirmed
  provider authentication and is not acceptable for this smoke.
- lzr historically had about 1.8 GiB available memory and no swap. It must run
  only the verified Remote Client, never Node, OpenCode, Docker or a build.

Resource gates are authoritative: local requires MemAvailable >= 8 GiB and
SwapFree >= 4 GiB before Node/Chromium/Docker; ASD requires MemAvailable >= 3
GiB before Go/OpenCode/image work and must pause AISummoner heavy work below 2
GiB; lzr requires MemAvailable >= 1.2 GiB, must stop AISummoner extras below 1
GiB, and must keep Client RSS below 150 MiB. Use one heavy job per host,
`GOMAXPROCS=2`, Go `-p 2`, `NODE_OPTIONS=--max-old-space-size=2048` and one
browser worker.

## Fixed Test Topology

| Host | Role | Ports and exposure |
| --- | --- | --- |
| local | Browser harness and conditional local OpenCode fallback | `127.0.0.1:18088`; fallback `127.0.0.1:14096/14097` only |
| ASD | Server, SQLite, Fake/OpenCode runtime, later isolated TLS proxy | Server `127.0.0.1:8088`; OpenCode `127.0.0.1:14096`; Bridge `127.0.0.1:14097`; public `443` only after a fresh free-port check |
| lzr | Linux Remote Client only | optional SSH-forward endpoint `127.0.0.1:18088`; no Client listener |

Use a UTC run ID and new explicit paths below
`/home/myself/.local/opt/aisummoner-task010/<run-id>` and
`/home/myself/.local/state/aisummoner-task010/<run-id>`. Never overwrite a
previous release or state directory.

## Ordered Execution Matrix

### 1. Read-Only Inventory

On all hosts capture UTC time, `hostname`, `uname -a`, `/proc/meminfo`, disk
space, scoped process inventory and `ss -H -ltnp`. On ASD additionally capture
nginx state/config hashes, Docker container inventory, DNS resolution and the
owners of ports 80/443/8088/14096/14097. On lzr confirm `x86_64` before
accepting the linux/amd64 artifact. A conflict on an internal port requires a
new private port and matching env values; a conflict on 443 stops the public
TLS phase and never authorizes changing nginx.

### 2. Clean Snapshot, Tests And Artifacts

Create a private isolated snapshot of the current working tree, excluding
`.git`, `.env`, runtime data, databases, logs, `node_modules`, `web/dist` and
build output. Do not use `git archive HEAD`, which can omit current uncommitted
MVP files. Record a sorted SHA-256 manifest.

Run locally, sequentially:

```sh
npm --prefix web ci
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Transfer the isolated source and `web/dist` to a private ASD staging directory
using `tar`/`scp` (ASD has previously lacked `rsync`). Run on ASD:

```sh
GOMAXPROCS=2 go test -count=1 -p 2 ./...
GOMAXPROCS=2 go vet ./...
```

Only inside staging, replace the tracked placeholder static assets with the
verified `web/dist`, then build:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOMAXPROCS=2 \
  go build -trimpath -p 2 -ldflags='-s -w' \
  -o out/aisummoner-server ./cmd/aisummoner-server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOMAXPROCS=2 \
  go build -trimpath -p 2 -ldflags='-s -w' \
  -o out/aisummoner-client ./cmd/aisummoner-client
```

Prove the Server does not contain the placeholder page. Record hashes on ASD,
after transfer to local, and after the Client is copied to lzr; lzr performs no
build.

There is currently no Playwright dependency or configuration in the
repository. Before browser acceptance, Task010 must add or select a reviewed,
lock-versioned Playwright/equivalent harness. Do not install an unpinned latest
package. Disable screenshots, video and traces while a pairing code or
credential is visible.

### 3. Private Bootstrap And Loopback Fake Topology

Use `umask 077` and independent random values for the administrator bootstrap
password, session compatibility secret, pairing secret, OpenCode Basic Auth
password and Bridge secret. Do not reuse host passwords. Keep env and browser
credential files at 0600 under 0700 directories; disable shell tracing and
never place secret values in command arguments, logs or documentation.

Initial Fake Server configuration is:

```text
AISUMMONER_BASE_URL=http://127.0.0.1:18088
AISUMMONER_LISTEN_ADDR=127.0.0.1:8088
AISUMMONER_DEV_MODE=1
AISUMMONER_AGENT_ADAPTER=fake
AISUMMONER_TRUSTED_PROXY_IPS=
```

After the first admin is created, stop the exact verified Server PID, create a
new runtime env without `AISUMMONER_ADMIN_PASSWORD`, remove the bootstrap env,
restart, and prove login succeeds. Start processes by sourcing a 0600 env file
inside the child rather than expanding secrets into argv.

Before external TLS mutation, establish two controlled SSH sessions:

```sh
ssh -M -S /tmp/as10-<run-id>-asd.sock -fnNT \
  -o ExitOnForwardFailure=yes \
  -L 127.0.0.1:18088:127.0.0.1:8088 ASD-Host
ssh -M -S /tmp/as10-<run-id>-lzr.sock -fnNT \
  -o ExitOnForwardFailure=yes \
  -R 127.0.0.1:18088:127.0.0.1:18088 lzr-host
```

The local Browser and lzr Client both use
`http://127.0.0.1:18088`; the Client uses `--dev` but still runs as `myself`,
never root. No nginx, firewall, Docker or public-port mutation is involved.

The reviewed Client systemd unit now uses a private `StateDirectory`,
`StateDirectoryMode=0700`, `UMask=0077`, and
`StandardOutput=append:/var/lib/aisummoner-client/pairing-output.log`, so pairing
codes do not intentionally enter the journal. Task010 must still dynamically
prove the file is owned by the service user at mode 0600, claim and reuse-test
the code, truncate the file immediately afterward, and prove the unit journal
contains neither `Pairing code:` nor the actual code. Do not reproduce the code
in acceptance output.

### 4. Browser -> ASD -> lzr Fake Acceptance

Capture the three hostnames first and make the lzr hostname the strict oracle.
The browser harness must cover:

1. Login, fresh code claim, second claim returning one-use failure, Device
   metadata and Online.
2. Terminal `hostname`, `uname -a`, `id -u`, unique stdout/stderr and explicit
   exit-code sentinels, plus one interactive `read` exchange.
3. Intercept the actual `terminal.resize` frame and compare its rows/cols with
   remote `stty size`.
4. Default per-command Fake Turn: approve `hostname` once, then separately
   approve `uname -a` once. Assert both tool states, exit 0,
   `truncated=false`, and final text containing lzr hostname/system evidence.
5. A second Session uses `approve_session`; later tools in that Session skip
   approval while another Session still asks. A third Session exercises deny.

The smoke must fail if output matches ASD or local. The Fake Adapter has two
default tool calls, so one approval is not a complete Fake Turn.

### 5. Reconnect, Restart And Newest-Wins

Stop only the scoped lzr Client and assert Offline within 15 seconds, existing
Terminal closure and Agent offline failure. Restart with the same identity and
assert Online without re-pairing. Record identity directory 0700, key/metadata
0600, stable Device ID, no listening Client socket and RSS below 150 MiB.

Restart the Server again without the bootstrap password and prove persisted
login/device/history behavior. For newest-wins, start a second scoped Client
with the same identity, prove the replacement can open a Terminal, then stop
the old instance promptly so the two reconnect loops cannot continuously
replace each other.

### 6. Real OpenCode Smoke

Prefer an ASD-local pinned OpenCode 1.18.11 installed only under the versioned
release. It must listen on `127.0.0.1:14096`; the Server Bridge must listen on
`127.0.0.1:14097`; both processes must run as the same unprivileged user and
see the same absolute 0700 workspace root and canonical 0600 policy/tool
bytes. Stop the Fake Server, start OpenCode, then restart the Server against
the same database with explicit `AISUMMONER_AGENT_ADAPTER=opencode` and all
required loopback/auth/model/Bridge values.

Prompt OpenCode to use only `remote_exec` for `hostname; uname -a`. Mark the
result exactly:

- `available`: real assistant text, actual tool approval/result, exit 0 and
  lzr hostname/uname proof;
- `rate_limited`: identifiable provider 429/rate-limit failure;
- `unavailable`: authentication, version, network or protocol failure.

Never substitute Fake output. A local authenticated OpenCode fallback is
allowed only if Task010 proves exact absolute workspace path and canonical
bytes on both sides, forwards ASD `14096` solely to local loopback and local
`14097` solely to the ASD Bridge, and keeps personal provider credentials on
local. If any workspace/auth/tunnel condition is unproven, record
`unavailable`; do not use SSHFS or copy credentials speculatively.

### 7. Isolated Public TLS

After a fresh 443/DNS check, restart the Server with its public HTTPS Base URL
while it remains bound to `127.0.0.1:8088`. Set
`AISUMMONER_TRUSTED_PROXY_IPS=127.0.0.1` for this TLS phase only. Generate a
Task010-only Caddy configuration with admin disabled, automatic redirects
disabled, HTTP challenge disabled and the following proxy boundary:

```caddyfile
reverse_proxy 127.0.0.1:8088 {
    header_up X-AISummoner-Client-IP {remote_host}
    flush_interval -1
}
```

Run it as an exactly named isolated 443-only container with host networking so
the Server's immediate Caddy peer is provably `127.0.0.1`; if host networking
cannot be used, stop and resolve an exact single container IP before changing
the trusted setting. Never trust a CIDR/subnet, `X-Forwarded-For`, or a dynamic
range. Do not use the standalone Compose example that also publishes port 80
and do not edit nginx.

Prefer a public certificate through TLS-ALPN. If that fails, a dedicated test
CA is acceptable only when Task010 copies its public root (never its private
key), scopes Browser trust to this endpoint and proves the production Client
trust path, for example through a private `SSL_CERT_FILE`. An
insecure-skip-verify Client is forbidden.

Restart lzr against the production HTTPS URL without `--dev`. Re-run login,
Secure/HttpOnly/SameSite=Strict cookie assertions, Terminal, Fake Agent,
Offline/reconnect and public reachability. From local and lzr, 8088/14096/14097
must be unreachable; ASD `ss` must show all three only on loopback. Port 80 and
nginx state/config hashes must match the preflight snapshot.

Before the functional rerun, verify proxy provenance without recording source
values: the Caddy upstream request has exactly one dedicated header; a direct
untrusted request with forged dedicated/XFF headers still uses its immediate
peer; malformed/missing header from the exact trusted peer gets 400 without
growing Login/Tunnel limiter state (the in-process regression is acceptable
for the private limiter-size observation); and exhausting source A does not
rate-limit source B for either Login or Tunnel.

### 8. Unpair, Expiry And Shutdown

For deterministic invalidation, return to Fake mode, hold one Terminal open
and one Agent Turn pending approval, then unpair. Assert HTTP 204, immediate
Device removal/offline, Terminal closure, Agent SSE/pending closure, old
Session/Tool NotFound behavior and a subsequent fresh code on Client
reconnect. If real expiry is exercised, wait beyond the fixed ten-minute
lifetime and require `PAIRING_CODE_EXPIRED`; record status/request ID and time,
never the code.

Use the approved in-process ownership tests for cross-owner evidence because
the deployed MVP intentionally has one administrator.

Shutdown in this order: scoped Client, SSH control sockets, exact Server PID,
exact OpenCode PID, exact Caddy container. Verify process identity before
signals; use SIGTERM and a bounded join first, and record a failure before any
SIGKILL of that exact PID. Never use broad `pkill`, globs or `compose down`.
Close SSH controls with `ssh -S <socket> -O exit <host>`. Delete a staging tree
only after validating its exact path and `.task010-stage` marker; retain the
private SQLite/state directory unless the human explicitly chooses removal.
Remove bootstrap files, pairing-output contents and temporary secret copies.

## Required Evidence

The acceptance record must include:

- source/artifact hashes, tool versions, exact commands, statuses and timings;
- host roles, non-secret endpoints, resource readings and component RSS;
- before/after ports, nginx hashes and unrelated container/process inventory;
- the exact trusted-proxy peer mode, dedicated-header overwrite check and
  two-source Login/Tunnel isolation result (without recording client IPs);
- restart without bootstrap password and private file modes;
- redacted pairing claim/reuse/expiry, post-claim truncation and journal scan;
- stable identity, no lzr listener, Offline/reconnect/newest-wins timing;
- Terminal location/PTY/resize evidence and Fake Agent tool/final evidence;
- honest OpenCode `available`/`rate_limited`/`unavailable` classification;
- unpair timestamps and immediate Tunnel/Terminal/Agent invalidation;
- cookie attribute booleans without cookie values;
- secret/sentinel absence checks for Server, Client, OpenCode, Caddy and
  journal logs;
- all 12 Baseline-0 acceptance rows labelled as live three-host, browser
  automation or approved in-process evidence;
- final running/cleanup state and exact scoped stop/recovery commands.

## Task008 Interfaces And Deployment Assumptions

Task010 relies on the approved Task008 result to provide:

- one shared Device gate wired into Gateway and `device.NewLifecycleService`,
  atomic Unpair revocation, joined Agent/Terminal/Tunnel invalidation and
  bounded SIGTERM shutdown;
- independent `AISUMMONER_BASE_URL` and loopback listen address, explicit
  Fake/OpenCode selection, custom OpenCode/Bridge loopback ports and no public
  Bridge route;
- a production build path that replaces the tracked placeholder with real
  Web assets; a plain clean-checkout Go build is not a production Web binary;
- immutable adapter selection at startup, so Fake/OpenCode transitions require
  graceful Server restarts against the same database;
- the fixed systemd pairing-output protections described above, which Task010
  still validates dynamically;
- normal system trust for the Client. A scoped test-CA path such as
  `SSL_CERT_FILE` must be proven during Task010 or the private-CA fallback is
  unavailable;
- stable accessible Web roles/selectors for a Task010 browser harness. The
  current absence of a pinned Playwright dependency remains a Task010
  prerequisite, not evidence that browser acceptance was run.
