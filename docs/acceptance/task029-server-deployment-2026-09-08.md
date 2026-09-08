# Task029 ASD Server Deployment (2026-09-08)

This record documents the user-authorized deployment of the reviewed Task029
Server build to the existing ASD test host. It is a bounded update of the
single-node Alpha test service, not a public production or Windows support
claim.

## Deployment

| Item | Result |
| --- | --- |
| Source commit | `096cf52f387f3e4dd41de3f655ff6f31cf916fa5` |
| Candidate | `/home/myself/.local/opt/aisummoner-task029/20260908T1838Z/bin/aisummoner-server` |
| Candidate SHA-256 | `ffee448d0ed1e3f059886adb06d123257642fd10cd6546af418b519c9a8d61c3` |
| Unit | `aisummoner-task029-server-20260908T1838Z.service` |
| Runtime user | `myself:myself` |
| Listen address | `127.0.0.1:8088` |
| Environment/database | Existing Task011 `server.env` and SQLite WAL database |
| Rollback | `/home/myself/.local/state/aisummoner-task029/rollback/20260908T1838Z` |

The old `aisummoner-task011-server-20260821T090612Z.service` was stopped only
after the candidate was staged. The old binary, unit metadata, environment
copy and hash manifest remain in the mode-0700 rollback directory. The old
deployment was not deleted.

## Verification

- New unit is `active`; the old unit is `inactive`.
- `http://127.0.0.1:8088/healthz` returned `{"status":"ok"}`.
- `https://122.51.70.33:10001/healthz` returned HTTP 200 through Caddy.
- Exactly one Server process owns `127.0.0.1:8088`; Caddy remains the only
  listener on `*:10001`.
- DSH (`127.0.0.1:14196`), the AISummoner Capability Bridge
  (`127.0.0.1:14197`) and OpenCode (`127.0.0.1:14096`) remained present.
- The existing SQLite database passed read-only `PRAGMA quick_check` (`ok`).
- The served Web assets match the local production build:
  - `index-DXyp30Ro.js`: `9756c1ed37d87eb52189d90c41867eec0da125b548029eda9b143421cff63b2b`
  - `index-B4FI4e_F.css`: `21cd0147818c276793c6b2cc79f2a9b666fa426a55c67807ba9e6f5f857f7492`
- Unrelated ASD containers remained running, including `mychat-postgres`,
  `asd-kgrag-*` and the AISummoner OpenCode container.

## TLS Renewal

The prior Let's Encrypt IP certificate was expired (`2026-08-28`). The
existing pinned Certbot lineage produced a replacement certificate with the
exact `IP:122.51.70.33` SAN, valid from 2026-09-08 07:31:45 UTC through
2026-09-14 23:31:44 UTC. It was installed in the existing Caddy TLS directory
after offline Caddy configuration validation, and Caddy alone was restarted.
The old certificate/key remain as `previous-public.crt` and
`previous-public.key`.

The renewal helper's direct self-IP curl check failed because ASD cannot route
to its own public address; its rollback branch restored the old files. A local
`--resolve 122.51.70.33:10001:127.0.0.1` check then verified the replacement
before it was kept active. An external strict curl to the public IP returned
HTTP 200 with the renewed certificate.

## Boundary

This deployment does not change the Server trust model, ports, database
schema, Remote protocol or Agent capability boundary. Windows remains an
unsigned engineering artifact with clean Windows 10/11, installer,
Authenticode and literal cross-machine Browser/pairing/Agent gates open.
