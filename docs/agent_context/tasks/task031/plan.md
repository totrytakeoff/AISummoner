---
task_id: task031
type: plan
status: proposed
from: coder
to: human_reviewer
revision: 0
requires_review: true
---

# Task 031 Plan: ASD Deployment Hygiene And Reboot Survival

## Objective

Make the ASD Alpha deployment reboot-survivable, repeatably verifiable and
clearly documented. Today every runtime component is either a transient
`systemd-run` unit, a bare child process, or a Docker container with
`restart=no`, so a host reboot would leave the Server, DSH, OpenCode, Caddy and
certificate renewal all down. This task fixes the lifecycle without changing
the Server binary, database, DSH/OpenCode configuration, or any trust boundary.

## Verified Current State (read-only, 2026-09-16)

- Server: transient unit `aisummoner-task029-server-20260908T1838Z.service`
  (active), `EnvironmentFile` points at the legacy
  `.../aisummoner-task011/20260821T090612Z/runtime/server.env`.
- DSH: child of the Server (`internal/dsh.StartHost`); no separate unit needed.
- Capability Bridge (14197): in-process with the Server.
- OpenCode: container `aisummoner-task011-opencode-20260821t090612z`,
  `restart=no`.
- Caddy: container `aisummoner-task011-caddy-20260821t090612z`, `restart=no`.
- Cert renewal: transient `aisummoner-task011-cert-renew-20260821T1025Z.timer`
  + `.service` (12h).
- Distro `certbot.timer`/`certbot.service` exist but `/etc/letsencrypt` has no
  `live/` or `archive/`, so it is a harmless no-op (documented, left enabled).

## Scope

### 1. Persistent systemd units

Create under `/etc/systemd/system/` (stable names so a future deploy updates
the same unit rather than appending a new transient one):

- `aisummoner-server.service` — replicate the transient Service block exactly
  (User/Group `myself`, `WorkingDirectory`, `EnvironmentFile`, `ExecStart`,
  `Restart=on-failure`, `RestartSec=2s`, `TimeoutStopSec=20s`,
  `KillMode=control-group`, `UMask=0077`, append logs). Add
  `[Unit] After=network.target` and `[Install] WantedBy=multi-user.target`.
- `aisummoner-cert-renew.service` — `Type=oneshot`,
  `ExecStart=.../renew-public-cert.sh`.
- `aisummoner-cert-renew.timer` — replicate `OnActiveSec=12h`,
  `OnUnitActiveSec=12h`, `AccuracySec=5min`, `RandomizedDelaySec=30min`;
  `[Install] WantedBy=timers.target`.

### 2. Docker restart policies

- `docker update --restart=unless-stopped aisummoner-task011-opencode-20260821t090612z`
- `docker update --restart=unless-stopped aisummoner-task011-caddy-20260821t090612z`

### 3. Deployment smoke script

Add `deploy/asd-smoke.sh` to the repo and deploy it to ASD. Read-only checks:

- Server loopback `/healthz` == 200.
- HTTPS `/healthz` via `--resolve 122.51.70.33:10001:127.0.0.1` == 200.
- TCP listeners present on 14196 (DSH), 14197 (bridge), 14096 (OpenCode).
- Exactly one listener on 8088.
- SQLite `PRAGMA quick_check` == `ok` (read-only, path from `server.env`
  `AISUMMONER_DATA_DIR`; prefer `sqlite3`, fall back to `python3`).
- Caddy and OpenCode containers running, exactly one each.
- Server process runs as `myself`.
- Never prints environment values, credentials or Terminal/Agent content.

### 4. Deployment record

Write a machine-readable `deployment.info` (mode 0600) capturing: source
commit, run id, server unit, binary path, environment file, data dir, DSH/
bridge/OpenCode endpoints, Caddy container, certificate lineage, and rollback
paths. This fills the gap left by the ad-hoc Task029 deployment.

## Switch Procedure (brief server downtime, no active Remote clients)

1. Write the four unit files, then `systemctl daemon-reload`.
2. Stop the transient cert-renew timer/service.
3. Stop the transient server unit; immediately `systemctl enable --now
   aisummoner-server.service`.
4. `systemctl enable --now aisummoner-cert-renew.timer`.
5. Apply the two `docker update --restart=unless-stopped` changes.
6. Run the smoke script; confirm DSH (14196) is re-spawned by the new Server.

Rollback: the transient units are only stopped, not deleted; if the persistent
server fails health, `systemctl start aisummoner-task029-server-20260908T1838Z.service`
restores the previous process.

## Verification

- `systemctl is-enabled` returns `enabled` for the server and cert-renew timer;
  both units are persistent (no longer under `/run/systemd/transient`).
- `systemd-analyze verify` passes for the new units.
- A controlled stop/start of `aisummoner-server.service` returns `/healthz`,
  DSH, bridge, OpenCode and Caddy to healthy (proves the boot path without a
  full host reboot).
- Smoke script exits 0 with every check green.
- No host reboot is performed (ASD also hosts unrelated projects).

## Out Of Scope (documented residuals)

- Physical rename of the legacy `aisummoner-task011` state directory, the
  Caddy/OpenCode container names, or the cert lineage paths: a risky migration
  with cosmetic benefit; recorded in `deployment.info` instead.
- OpenCode Basic Auth password and bridge secret currently visible via
  `docker inspect` container Env (pre-existing). Not reproduced in docs;
  secret-storage redesign belongs to a later ADR-0006 follow-up.
- Changing the distro `certbot.timer` (harmless no-op, left enabled).
- Server binary, database schema, DSH/OpenCode configuration, Remote protocol,
  and Windows release work.
