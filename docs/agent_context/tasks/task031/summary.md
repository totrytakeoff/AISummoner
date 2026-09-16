---
task_id: task031
type: summary
status: completed
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 031 Summary: ASD Deployment Hygiene And Reboot Survival

## Outcome

The ASD Alpha deployment is now reboot-survivable and repeatably verifiable.
Persistent systemd units replace the transient `systemd-run` units, both Docker
runtime containers gained a restart policy, and a read-only smoke script plus a
machine-readable `deployment.info` record the current mapping. No Server
binary, database, DSH/OpenCode configuration, or trust boundary changed.

## Verified Findings

- DSH is a Server-managed child process (`internal/dsh.StartHost`), so it does
  not need its own unit; the persistent Server re-spawns it on start.
- The Capability Bridge (14197) is in-process with the Server.
- Caddy and OpenCode were Docker containers with `RestartPolicy=no`.
- The distro `certbot.timer`/`certbot.service` is a harmless no-op
  (`/etc/letsencrypt` has no `live/` or `archive/`); left enabled.

## Changes Applied

1. Persistent units under `/etc/systemd/system/` (all `enabled`):
   - `aisummoner-server.service` — replicates the transient Service block
     (User/Group `myself`, `EnvironmentFile`, `ExecStart`, `Restart=on-failure`,
     `RestartSec=2s`, `TimeoutStopSec=20s`, `KillMode=control-group`,
     `UMask=0077`, append logs) plus `After=network.target` and
     `WantedBy=multi-user.target`.
   - `aisummoner-cert-renew.service` — `Type=oneshot`, runs
     `renew-public-cert.sh`.
   - `aisummoner-cert-renew.timer` — 12h cadence (`OnActiveSec`/
     `OnUnitActiveSec`, `AccuracySec=5min`, `RandomizedDelaySec=30min`),
     `WantedBy=timers.target`.
2. Docker restart policies: Caddy and OpenCode containers updated to
   `--restart=unless-stopped`.
3. Smoke script `deploy/asd-smoke.sh` (repo) deployed to the task029 state
   directory. It checks server/HTTPS health, DSH/bridge/OpenCode listeners,
   single 8088 listener, SQLite `quick_check` (read-only, python3 fallback),
   container liveness, server process user, and serving-cert expiry.
4. `deployment.info` (mode 0600) records source commit, run id, unit names,
   binary/env/data/tls/LE paths, endpoints, containers, and legacy transient
   unit names.

## Defect Fixed During Switch

The switch exposed a latent ownership inconsistency: the task029 runtime
directories (`opt/.../20260908T1838Z` and `state/.../20260908T1838Z`) were
`root:root 0700`, unlike task011/task020 which are `myself:myself`. Starting
the persistent unit as `myself` therefore failed with `status=200/CHDIR`.
Both trees were `chown -R myself:myself`, matching the established pattern, and
the server then started cleanly and re-spawned DSH (parent PID verified).

## Verification

- `systemctl is-enabled` returns `enabled` for the server and cert-renew
  timer; only the three persistent units remain (transient units gone).
- `systemd-analyze verify` passes for all new units.
- A controlled stop/start of `aisummoner-server.service` returned `/healthz`,
  DSH (14196), bridge (14197), OpenCode (14096) and Caddy to healthy.
- Smoke script exits 0 with 11/11 checks PASS.
- Strict TLS `https://122.51.70.33:10001/healthz` returns HTTP 200 from the
  workstation and from lzr-host.
- Timer next run: 2026-09-17 11:00:44 CST.

## Residuals (documented, out of scope)

- Legacy `task011` naming on the shared state directory, containers, and
  certificate lineage is documented in `deployment.info`, not physically
  renamed (risky migration, cosmetic benefit).
- OpenCode Basic Auth password and bridge secret remain visible via
  `docker inspect` container Env (pre-existing; secret-storage redesign belongs
  to a later ADR-0006 follow-up).
- The distro `certbot.timer` remains enabled (harmless no-op).
- No full host reboot was performed (ASD also hosts unrelated projects); the
  controlled unit restart and Docker restart policies cover the boot path.
