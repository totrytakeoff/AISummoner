# Task010 Revision 2 Cleanup Record — 2026-08-13

Run ID: `20260813T072250Z`.

This cleanup followed the independent revision-2 `APPROVED` decision. It did
not alter revision-0 audit state, nginx, port 80, any shared Docker network or
any unrelated workload.

## Exact Component Shutdown

- lzr Client PID `595619` was revalidated by process start time, exact scoped
  executable path and uid 1003, then received TERM and joined. It owned no
  listening socket.
- ASD Server PID `1594510` was revalidated by process start time, exact scoped
  executable path and uid 1001, then received TERM and joined. Its loopback
  8088 listener disappeared.
- Caddy container
  `57646701ad0838148454451c86be533b461e5fd8446b6b66d19d50466a212008`
  was revalidated by exact name/ID and `restart=no`, stopped with a 20-second
  grace, and removed. Public 10001 and unused 10002 were both free afterward.
- Final `/proc/*/exe` enumeration found zero processes using either revision-2
  binary. ASD listeners on 8088/10001/10002/14096/14097 and revision-2
  containers were zero. An earlier `ps | grep <run-id>` check counted its own
  SSH/shell/grep commands as three matches on each host; it was rejected as a
  false-positive oracle and replaced by the exact executable check.

## Secret And Test-Artifact Cleanup

- ASD runtime/browser env files, CA private/public material, leaf private key
  and certificate, transient Caddy config/state, and PID/container markers
  were deleted. The secret and TLS directories are absent.
- lzr copied CA, already-empty pairing-output file, and Client PID markers were
  deleted.
- The local private controller directory, env/CA/control markers,
  `web/e2e/node_modules`, and the non-secret Playwright `passed` marker/output
  directory were deleted.
- The repository contains no runtime `.env`, key, certificate, database,
  pairing-output or log artifact. `.env.example` remains the public template.

The fresh SQLite database, lzr Device identity, verified binaries and scoped
non-secret logs were retained under the run-owned 0700 directories for audit
or a future fresh-secret recovery. They are not running and expose no port.

## Unrelated-Service Preservation

nginx remained active and all six configuration hashes matched the preflight
baseline. The same four unrelated ASD containers—`asd-kgrag-qa`,
`asd-kgrag-qdrant`, `asd-kgrag-neo4j`, and `mychat-postgres`—remained running.
No nginx file, unrelated container, firewall rule or revision-0 audit path was
targeted.
