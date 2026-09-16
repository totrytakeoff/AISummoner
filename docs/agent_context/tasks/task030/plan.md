---
task_id: task030
type: plan
status: proposed
from: coder
to: human_reviewer
revision: 0
requires_review: true
---

# Task 030 Plan: Repair ASD Public TLS Certificate Renewal And Activation

## Objective

Restore the expired public HTTPS entry on ASD and make certificate renewal
self-activating. The Let's Encrypt lineage is healthy (cert8, valid through
2026-09-22, is already on disk) but the custom renew helper's final public
self-loop check always fails on this cloud VM, so every renewal is rolled back
and the serving certificate stays expired. This task activates the already
issued certificate, fixes the verification step, and proves the renew path
works end to end.

## Root Cause (verified read-only on 2026-09-16)

- Serving cert `.../tls/server.crt` is the 2026-09-08 cert, `notAfter=Sep 14
  23:31:44 2026 GMT`. It is expired.
- `.../letsencrypt/config/archive/aisummoner-task011-ip/fullchain8.pem` holds a
  valid cert `notBefore=Sep 15 11:17:34, notAfter=Sep 22 03:17:33 2026 GMT`.
- `renew-public-cert.sh` does: `certbot renew` -> verify/checkip -> install new
  fullchain/privkey -> offline Caddy validate -> `docker restart caddy` ->
  `curl https://122.51.70.33:10001/healthz`.
- The final `curl` targets the host's own public IP. ASD cannot route to its
  own public address, so this check fails, the script runs its rollback branch,
  and the new certificate is reverted. This is why the renew service is in
  `failed` state and the serving cert never advances.

## Scope

Only ASD certificate activation and the renew helper. No Server binary,
database, DSH/OpenCode, Caddy topology, or Remote/Agent protocol changes.

## Steps

### Step 1: Activate the already-issued certificate

Run on ASD as root:

1. Keep the expired cert as rollback:
   `install -m 600 -o myself -g myself tls/server.crt tls/rollback-expired.crt`
   and same for `tls/server.key -> tls/rollback-expired.key`.
2. Install cert8 into the serving path:
   `install -m 600 -o myself -g myself letsencrypt/config/live/.../fullchain.pem tls/server.crt`
   and `privkey.pem -> tls/server.key`.
3. Offline Caddy validation (same command as the script, `--network none`).
4. `docker restart aisummoner-task011-caddy-20260821t090612z`.
5. Verify locally through the real TLS path without external routing:
   `curl -fsS --resolve 122.51.70.33:10001:127.0.0.1 https://122.51.70.33:10001/healthz`.

### Step 2: Fix the renew helper's final verification

Edit `renew-public-cert.sh` so the post-restart check no longer depends on the
host reaching its own public IP:

- Replace the final `curl https://122.51.70.33:10001/healthz` with a bounded
  retry loop using `curl -fsS --resolve 122.51.70.33:10001:127.0.0.1
  https://122.51.70.33:10001/healthz`. `--resolve` keeps the `122.51.70.33`
  Host header (required by the Caddy host matcher) while connecting to
  loopback.
- Keep the existing offline Caddy validate, the rollback branch, and `exit 1`
  on verification failure.
- Keep `set -eu` and the flock guard unchanged.

### Step 3: Add an expiry alarm

Append a small check so a near-expiry or already-expired serving cert is
reported instead of silently passing. Use `openssl x509 -checkend` against the
serving cert; on failure, log a warning to stderr. This does not change the
rollback semantics.

### Step 4: Prove the renew path

1. `systemctl reset-failed aisummoner-task011-cert-renew-20260821T1025Z.service`.
2. Run the renew helper once manually and confirm it now completes successfully
   (certbot may report "not due for renewal"; the script must still activate
   any newer live cert and pass the loopback check).
3. Confirm the timer `aisummoner-task011-cert-renew-20260821T1025Z.timer` is
   still enabled with a future next-run time.

## Verification

- `https://122.51.70.33:10001/healthz` returns HTTP 200 with strict TLS from
  the workstation (and from lzr-host as an external second vantage).
- `openssl s_client` shows the serving cert `notAfter` moved from Sep 14 to
  Sep 22 (or newer).
- The renew service is no longer `failed` after a manual run.
- No Server/DSH/OpenCode unit or container was restarted or modified; only the
  AISummoner Caddy container is restarted during the switch.

## Rollback

- The expired serving cert/key remain as `rollback-expired.crt/.key`.
- The previous `renew-public-cert.sh` is copied to
  `renew-public-cert.sh.pre-task030` before editing.
- If the loopback verification fails, reinstall the previous cert and restart
  Caddy; the script's own rollback branch stays intact.

## Out Of Scope

- Task011 naming residue, transient systemd unit persistence across reboot,
  Caddy `restart=no`, and the deployment smoke script: these move to a separate
  deployment-hygiene task (task031).
- Let's Encrypt challenge/webroot topology, short-lived profile policy, ARI
  tuning, and anything touching the `asd-kgrag` well-known directory.
- Server binary, database, DSH/OpenCode, Remote protocol, Windows release work.
