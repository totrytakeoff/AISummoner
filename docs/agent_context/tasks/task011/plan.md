---
task_id: task011
type: plan
status: implemented
from: orchestrator
to: implementation
revision: 0
requires_review: false
---

# Task 011 Plan: Persistent Test Deployment And AppImage

## Status

Implemented and ready for user testing.

## Goal

Leave a bounded AISummoner test environment ready for manual use:

1. deploy the reviewed Fake-Agent Server on ASD without changing nginx or any
   unrelated workload;
2. produce and locally verify a Linux x86_64 AppImage wrapping the reviewed
   Remote Client binary;
3. start a non-root Remote Client on the local workstation so another device
   can pair with and control that workstation through the ASD Server.

## Deployment Boundary

- Recheck every listener and process identity immediately before mutation.
- Preserve ASD nginx, TCP 80, all unrelated containers and existing Task010
  audit state.
- Use a fresh run-owned directory, database, credentials and TLS material.
- Server listens only on ASD loopback `127.0.0.1:8088`.
- A single scoped Caddy container may expose only the already-authorized,
  currently-unused TCP 10001 after a fresh reachability check.
- Use Fake Agent for deterministic user testing; do not start OpenCode.
- Run Server and local Client as non-root users.
- Keep secrets out of argv, logs, repository and final chat output. Put the
  user handoff in a local mode-0600 directory.

## AppImage Scope

- Package the existing Linux amd64 CLI as a headless AppImage; do not add a
  GUI, daemon, auto-update, root bypass or protocol change.
- `AppRun` forwards all arguments to `aisummoner-client`.
- Include only public metadata/icon assets and the reviewed Client binary.
- Verify the embedded binary hash, AppImage extraction, CLI usage and a real
  connection to the deployed Server.

## Verification

- Exact listener/process/container checks before and after deployment.
- Strict TLS health through ASD TCP 10001, including a negative system-trust
  check and positive scoped-CA check.
- Server runs as ASD uid 1001; local Client runs as local uid 1000 and owns no
  listening socket.
- Browser login page is reachable and the fresh local Device receives a
  pairing code.
- AppImage embedded Client hash equals the reviewed standalone Client hash.
- No secret value appears in repository paths or the final response.

## Handoff

Record the public URL, public test CA, administrator username/password,
pairing code, AppImage and SHA-256 checksums in a local private handoff
directory. Provide start/stop/status instructions and exact cleanup targets.

## Out Of Scope

- Publicly trusted production PKI, SLA or long-term production hardening.
- Modifying firewall/cloud rules, nginx, port 80 or unrelated containers.
- OpenCode deployment, other operating systems, automatic installation or
  automatic updates.
