# Task010 Scoped Cleanup Record — 2026-08-13

This record was written after the independent Task010 review froze the
acceptance snapshot. It does not change that review's `BLOCKED` decision or
the hashes it verified.

Run ID: `20260813T024159Z`.

## Result

- lzr Client PID `524331` was revalidated by uid `1003`, exact scoped
  executable path and SHA-256, then received `SIGTERM` and joined within the
  bounded wait. It had no child and no listening socket.
- The lzr scoped relay PID `454847` was revalidated by uid, Python executable,
  exact run-owned script and start identity, then received `SIGTERM` and
  joined within the bounded wait.
- ASD Server PID `1530187` was revalidated by uid `1001`, exact scoped
  executable path and SHA-256, then received `SIGTERM` and joined within the
  bounded wait.
- Only the exact Task010 Caddy and OpenCode containers were stopped with a
  20-second grace and removed. Both had `restart=no` and container IDs matching
  their scoped markers. No `compose down`, shared-network deletion or broad
  process match was used.
- Before closing SSH, ASD had zero Task010 processes, zero Task010 containers,
  and zero listeners on 443/8088/14096/14097. lzr had zero Task010 processes
  and zero Client listeners. nginx remained active.
- Both exact SSH master sockets were closed with `ssh -S <socket> -O exit` and
  disappeared. The local scoped HTTPS forward was no longer reachable, and no
  local Task010 process remained.

## Sensitive Temporary Material

Exact-file cleanup removed:

- ASD runtime env, sidecar env, container/PID markers, test-CA private key,
  leaf private keys/certificates and transient TLS configuration (21 paths).
- lzr PID/relay markers, copied test CA and four already-empty pairing-output
  files (11 paths).
- local browser env files, admin/pairing-code copies, copied test CA and
  orchestration helpers/control markers (13 paths).

The private ASD SQLite database, structured logs, verified artifacts/source
and lzr Device identity were intentionally retained under their 0700 scoped
run directories for recovery and audit. The repository contains none of the
18 runtime credential/private-key values checked by the final secret scan.

## External Blocker

No runtime is left running. To resume, an operator must first permit inbound
TCP 443 at the cloud/upstream boundary. Then recreate fresh secrets and a
public certificate, start only the exact scoped Server/Caddy/OpenCode/Client
topology, and rerun direct public health/login/Client WSS/Terminal WSS/Agent
SSE before returning Task010 to independent review.

The final all-container inventory could not be resampled after the SSH masters
were closed because a new SSH connection was rejected by the workstation's
network proxy. This does not weaken the cleanup actions: immediately before
master closure the exact Task010 process/container/listener counts were zero,
and the independent reviewer had already confirmed nginx hashes and the
pre-existing workloads were unchanged. No unrelated container was targeted by
any cleanup command.
