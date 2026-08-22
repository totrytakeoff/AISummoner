# AISummoner browser acceptance harness

This package is a single-worker Playwright harness for Task010's deterministic
Browser → Server → Remote acceptance. It does not start a Server, Client,
OpenCode, SSH tunnel, proxy, or remote process. The main Task010 integration
agent owns those actions and advances this harness only through fixed,
non-secret marker files.

The dependency is locked exactly to `@playwright/test` 1.60.0. The configuration
forces one Chromium worker and disables retries, screenshots, video, traces,
downloads, service workers, rich reports and retained test output. Test helpers
replace raw Playwright assertion output with stage names and boolean oracles;
they never print credentials, pairing codes, HTTP bodies, Agent events or
Terminal output.

## Runtime contract

Create a new local control directory owned by the harness user, mode `0700`,
with no symlink components at its final path. Export the variables below from a
private mode-`0600` file or an inherited process environment while shell tracing
is disabled. Never put their values in command arguments, repository files or
acceptance output.

| Variable | Meaning |
| --- | --- |
| `AISUMMONER_E2E_BASE_URL` | Credential-free HTTPS origin, or loopback HTTP origin for the private phase. |
| `AISUMMONER_E2E_PHASE` | `fake-lifecycle` for the primary run; `reclaim` for the post-unpair fresh-code run; `tls-smoke` for a non-destructive current-device HTTPS/WSS/SSE check; `opencode-smoke` for the separately classified real-provider turn. |
| `AISUMMONER_E2E_USERNAME` | Runtime administrator username. |
| `AISUMMONER_E2E_PASSWORD` | Runtime administrator password. |
| `AISUMMONER_E2E_PAIRING_CODE` | Current one-use Remote pairing code. It is consumed and reuse-tested in the selected phase. |
| `AISUMMONER_E2E_REMOTE_HOSTNAME` | lzr hostname oracle. |
| `AISUMMONER_E2E_SERVER_HOSTNAME` | ASD hostname negative oracle; must differ from remote/local. |
| `AISUMMONER_E2E_LOCAL_HOSTNAME` | Browser host negative oracle; must differ from remote/server. |
| `AISUMMONER_E2E_REMOTE_UID` | Expected numeric UID of the unprivileged lzr Client user. |
| `AISUMMONER_E2E_CONTROL_DIR` | Absolute owned `0700` real directory for fixed barrier markers. |
| `AISUMMONER_E2E_BARRIER_TIMEOUT_MS` | Optional `30000`–`900000`; default `300000`. |
| `AISUMMONER_E2E_ALLOW_SCOPED_TLS_ERRORS` | Optional `1`, HTTPS only, for Task010's scoped test-CA endpoint. Normal public TLS leaves it unset. |

The harness intentionally validates only that hostname oracles are distinct;
it does not log them. The main agent must separately record their non-secret
values and exact host roles in the acceptance record.

## Main-agent barriers

For each barrier, the harness atomically creates a mode-`0600` file named
`ready-<name>` containing exactly `ready\n`, then waits for a main-agent-created
mode-`0600` file `continue-<name>` containing exactly `continue\n`. Both files
must be owned by the harness user. Marker names and contents are public and
fixed; no PID, path, code, token or other runtime value crosses this channel.

The main integration agent must wait for the ready marker, perform only the
scoped operation below, verify its postcondition independently, and atomically
publish the continue marker. It must never pre-create continue markers.

| Barrier | Main-agent operation before `continue-*` |
| --- | --- |
| `stop-client` | Stop only the verified primary lzr Client and wait for exact process exit. |
| `restart-client` | Restart the same identity and verify the expected PID/identity before continuing. |
| `start-replacement-client` | Start a second scoped Client with the same identity and wait for authenticated replacement. |
| `stop-old-client` | Stop only the old instance while leaving the replacement running. |
| `restart-server` | Gracefully restart only the scoped Server without bootstrap password and wait for health plus Client reconnect. |
| `fresh-pairing-code` | After unpair, observe a fresh private pairing output, truncate the remote pairing log, and retain the code only in the next run's private environment. This barrier ends the primary phase. |

Each run needs an empty new control directory. The main agent removes its
markers during scoped Task010 cleanup only after the Playwright process exits.

## Phases and evidence

`fake-lifecycle` covers browser login/cookie flags, unauthenticated/Origin/
unknown-resource errors, claim and one-use failure, metadata/Online state,
Terminal remote-location and PTY/resize behavior, Fake Agent approvals,
Offline/reconnect/newest-wins/Server restart, and unpair invalidation. Fake's
two default tools are independently approved and checked for exit `0` and
`truncated=false`. `approve_session` must cause its second tool to run without
a second pending event; another Session still requires decisions and exercises
deny.

`reclaim` starts as a new Playwright process with the newly issued pairing code.
It reclaims the device, reuse-tests that fresh code, and proves the Terminal is
still lzr. Keeping the fresh code out of marker files avoids persistence or
accidental output from the first process.

`tls-smoke` does not pair, unpair, stop, or restart anything. It logs in over
the configured HTTPS origin, inspects the Secure/HttpOnly/Strict cookie, selects
the single current Online lzr Device, runs the Terminal PTY/resize checks, and
completes one two-tool Fake Agent turn over SSE. It is intended for Task010's
scoped test-CA fallback after the production Client has independently completed
strict CA validation; `AISUMMONER_E2E_ALLOW_SCOPED_TLS_ERRORS=1` remains limited
to that isolated browser context and credential-free HTTPS origin.

`opencode-smoke` is also non-destructive. It creates one per-command Agent
Session against the current Online lzr Device, asks the real OpenCode provider
for exactly one `remote_exec` call that returns hostname and operating-system
evidence, approves it once, and requires both the tool result and final
assistant message to identify lzr rather than ASD/local. Run it only after the
main agent has independently classified sidecar health/model availability and
confirmed that the Server is actually in `opencode` mode.

Terminal commands and bounded Agent-event copies exist only in memory and are
used through boolean predicates. The harness does not attach them on failure.
Task010's real OpenCode classification, public-TLS topology checks, log scans,
remote resource checks and cleanup remain main-agent/manual evidence; this
package does not pretend to automate them.

## Controlled invocation

The main agent first enforces the repository resource gate (`MemAvailable >= 8
GiB`, `SwapFree >= 4 GiB`), sets `NODE_OPTIONS=--max-old-space-size=2048`, and
runs one heavy job at a time. Dependency and browser installation are explicit:

```sh
npm --prefix web/e2e ci --ignore-scripts --no-audit --no-fund
npm --prefix web/e2e exec -- playwright install chromium
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web/e2e test
```

The first two commands are omitted when a reviewed, matching Playwright 1.60.0
installation and browser cache are already proven. Do not add reporter flags,
debug mode, UI mode, tracing, screenshots or video. A non-zero exit records
only the failed stage; inspect live scoped services rather than enabling
secret-bearing artifacts.
