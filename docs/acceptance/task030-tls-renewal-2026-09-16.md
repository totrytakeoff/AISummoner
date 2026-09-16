# Task030 ASD TLS Renewal Repair (2026-09-16)

Bounded repair of the ASD public HTTPS entry and the AISummoner certificate
renewal helper. No Server binary, database, DSH/OpenCode, Caddy topology, or
Remote/Agent protocol change.

## Problem

The serving certificate (`tls/server.crt`) was the 2026-09-08 cert, expired
2026-09-14 23:31:44 UTC. Let's Encrypt had already issued a replacement (cert8,
valid through 2026-09-22), but `renew-public-cert.sh` always rolled back its
own activation because its final verification curl targeted the host's own
public IP, which this cloud VM cannot route to.

## Fix

| Item | Result |
| --- | --- |
| Activated cert | `fullchain8.pem`/`privkey8.pem` -> `tls/server.crt/.key` |
| Serving cert | `notBefore=Sep 15 11:17:34, notAfter=Sep 22 03:17:33 2026 GMT` |
| Expired rollback | `tls/rollback-expired.crt/.key` |
| Script backup | `renew-public-cert.sh.pre-task030` (sha256 `d4e3013c…`) |
| Installed script | sha256 `6fc5c5b4…`, `root:root 0700` |
| Final check | self-IP curl -> `--resolve` loopback retry loop (15x1s) |
| Expiry alarm | `openssl x509 -checkend 172800` warns within 2 days |

## Verification

- Strict TLS `https://122.51.70.33:10001/healthz` returns HTTP 200 from the
  workstation and from lzr-host.
- Manual `renew-public-cert.sh` run exits 0; the cert-renew service is no
  longer `failed`.
- Renewal timer active, next trigger 2026-09-17 09:05:54 CST.

## Rollback

- `tls/rollback-expired.crt/.key` restores the pre-task state.
- `renew-public-cert.sh.pre-task030` restores the previous helper.

## Deferred To Task031

Transient unit reboot persistence, `task011` naming residue, Caddy
`restart=no`, the unrelated distro `certbot.timer`, and a deployment smoke
script.
