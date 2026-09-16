# Task031 ASD Deployment Hygiene (2026-09-16)

Makes the ASD Alpha deployment reboot-survivable and repeatably verifiable. No
Server binary, database, DSH/OpenCode configuration, or trust-boundary change.

## Before

Transient `systemd-run` units for the Server and certificate renewal, Docker
containers with `RestartPolicy=no`, and no machine-readable deployment record.

## After

| Item | Result |
| --- | --- |
| Server unit | `aisummoner-server.service` (persistent, enabled) |
| Cert renew | `aisummoner-cert-renew.service` + `.timer` (persistent, enabled, 12h) |
| Caddy container | `restart=unless-stopped` |
| OpenCode container | `restart=unless-stopped` |
| Smoke script | `deploy/asd-smoke.sh` (repo + ASD) |
| Deployment record | `state/aisummoner-task029/20260908T1838Z/deployment.info` (0600) |

## Defect Fixed

Task029 runtime directories were `root:root 0700` (inconsistent with
task011/task020), causing `myself`-run CHDIR failure. `chown -R myself:myself`
applied to the task029 `opt`/`state` trees.

## Verification

- Smoke script: 11/11 PASS.
- Strict TLS `/healthz` returns HTTP 200 from workstation and lzr-host.
- Controlled server restart re-spawns DSH (child), bridge, and restores
  OpenCode/Caddy.
- `systemd-analyze verify` passes; units `enabled`.

## Residuals

`task011` naming residue documented (not renamed); OpenCode secret in container
Env (pre-existing); distro `certbot.timer` left enabled (no-op). No full host
reboot performed.
