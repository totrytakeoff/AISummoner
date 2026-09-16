---
task_id: task030
type: summary
status: completed
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 030 Summary: Repair ASD Public TLS Certificate Renewal And Activation

## Outcome

The expired public HTTPS entry on ASD is restored, and the renew helper now
activates certificates without depending on the host reaching its own public
IP. The root cause was not the ACME renewal itself but the helper's final
self-loop check, which always failed on this cloud VM and triggered rollback.

## Root Cause (confirmed)

`renew-public-cert.sh` renewed the Let's Encrypt lineage successfully every
time (archive cert8 was valid through 2026-09-22), but its last step ran
`curl https://122.51.70.33:10001/healthz` against the host's own public IP.
ASD cannot route to its own public address, so the check failed, the script
ran its rollback branch, and the serving cert stayed on the expired
2026-09-08 cert (`notAfter=Sep 14 23:31:44 2026 GMT`).

## Changes Applied (ASD only, no code/binary/database/protocol changes)

1. Activated the already-issued cert8 into the serving path:
   - expired cert kept as `tls/rollback-expired.crt/.key`;
   - `fullchain8.pem`/`privkey8.pem` installed as `tls/server.crt/.key`;
   - offline Caddy validation passed; Caddy restarted; loopback TLS check
     returned `{"status":"ok"}`.
2. Fixed the helper's final verification: replaced the self-IP curl with a
   bounded 15x1s retry loop using
   `curl --resolve 122.51.70.33:10001:127.0.0.1 https://122.51.70.33:10001/healthz`.
   The `--resolve` keeps the `122.51.70.33` Host header (required by the Caddy
   host matcher) while connecting through loopback. Rollback branch unchanged.
3. Added a serving-cert expiry alarm using `openssl x509 -checkend 172800`
   (warns on stderr when the serving cert expires within 2 days).
4. Original script preserved as `renew-public-cert.sh.pre-task030`
   (sha256 `d4e3013cd010d0d78c4386f5fd840db9073a87cdc340df777094cec416e28927`).
   Installed script sha256
   `6fc5c5b4c35e2348992e0204fb6052e37129d8ae2b2aa61ade4ff79b8844fd89`,
   owner `root:root`, mode `0700`.

## Verification

- `https://122.51.70.33:10001/healthz` returns HTTP 200 with strict TLS from
  the workstation and from lzr-host (two external vantages).
- Serving cert advanced from `Sep 14` to `Sep 22 03:17:33 2026 GMT`
  (issuer Let's Encrypt YR2, SAN `IP:122.51.70.33`).
- Manual helper run completed with exit 0; the service unit is no longer in
  `failed` state.
- Timer `aisummoner-task011-cert-renew-20260821T1025Z.timer` remains active,
  next trigger 2026-09-17 09:05:54 CST.
- No Server/DSH/OpenCode unit, container, database, Caddy topology, or
  Remote/Agent protocol was changed; only the AISummoner Caddy container was
  restarted during the switch.

## Known Limits Carried To Task031 (deployment hygiene)

- The Server and cert-renew units are transient `systemd-run` units under
  `/run/systemd/transient`, so they do not survive a host reboot.
- Paths and the Caddy container still carry the `task011` naming residue.
- Caddy container has `RestartPolicy=no`.
- A separate distro `certbot.timer`/`certbot.service` also exists and was not
  audited; it is unrelated to the AISummoner lineage but should be reviewed in
  task031.
- The Let's Encrypt profile is short-lived (~7 day certs); the ~2 day alarm
  threshold was chosen to surface renewal problems before expiry while the
  timer still fires roughly twice a day.
