---
task_id: task024
type: plan
status: ready_for_review
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 024 Plan: Common Remote Core Platform Seams

## Status

Implementation was explicitly authorized by the user on 2026-08-31 after the
Task023 Windows Server 2022 contract run. Task023's ordinary-user Windows
11/10, wrong-logon and clean-VM evidence remains open, so this task does not
accept ADR-0007 or claim Windows Remote support.

## Goal

Move every production Linux primitive below a narrow build-tagged platform
boundary while preserving the proven Linux daemon, AppImage, Tunnel, Terminal
and Agent execution behavior. Extend the existing Tunnel hello metadata from a
Linux-only literal to the strict `linux|windows` enum required by the next Core
task.

This is an extraction task. It must leave concrete, testable interfaces for the
Windows backends in Tasks025-027, but must not retain a fake Windows backend
that reports success merely to make a cross-build green.

## Owner

Primary implementation agent, without delegated coding agents.

## Scope

### 1. Platform runtime seam

- Add `internal/clientplatform` as the production boundary for the current OS
  name, default per-user data directory, ordinary-user privilege gate and
  process shutdown notification.
- Keep the current Linux paths, root development override and SIGINT/SIGTERM
  behavior byte-for-byte compatible at the CLI boundary.
- Route `cmd/aisummoner-client` and `remoteclient.New` through this seam instead
  of direct `os.Geteuid`, `.local/share`, Unix signal and `platform=linux`
  literals.

### 2. Device Identity storage seam

- Keep Ed25519 generation, Device ID derivation, signing and SSH signer
  behavior in common code.
- Move PEM, mode-0700 directory, mode-0600 files and POSIX file inspection into
  a Linux store implementation selected by build tags.
- Preserve stable reload, metadata repair, mismatch rejection and fail-closed
  partial-state semantics.

### 3. Local IPC transport seam

- Keep newline-JSON framing, strict decoding, method dispatch, deadlines,
  handler bounds and controller error mapping in common code.
- Introduce a private transport/listener interface that owns endpoint
  validation, dial/listen and peer authentication.
- Move Unix socket creation, parent/inode/mode checks, stale cleanup and
  `SO_PEERCRED` same-UID authentication into the Linux transport.
- Authentication must still complete before the first request byte is read.

### 4. SSH session process seam

- Keep SSH handshake, client-key verification, channel/request parsing,
  payload/window limits, exit-status framing and joined Session lifecycle in
  common code.
- Introduce an execution backend and process contract for absolute-path
  recognition, cwd validation, exec, interactive shell, resize, signal,
  termination and joined completion.
- Move `/bin/sh`, `creack/pty`, Unix process groups/sessions, pidfd, `/proc` and
  signal cleanup into Linux build-tagged files without weakening descendant
  cleanup or resize/finalization ordering.

### 5. Tunnel platform enum

- Define the supported Device platforms once in `internal/protocol`.
- Allow exactly `linux` and `windows` in Client metadata and Server hello
  validation; reject empty, unknown and NUL-containing values.
- Add mutation-sensitive tests proving Windows acceptance and unknown-platform
  rejection. Protocol version remains `1`.

## Non-Goals

- No DPAPI identity, named-pipe IPC or Windows token/path implementation; those
  are Task025.
- No PowerShell/Job Object exec or ConPTY; those are Tasks026-027.
- No Windows GUI/ZIP, target-aware Server cwd or Agent Execution Profile.
- No Server deployment, ASD mutation, database migration, WebUI or Runtime
  adapter change.
- No general plugin registry or speculative macOS backend.

## Required Tests

- Focused unit tests for the platform, Identity store, IPC dispatch/transport,
  SSH common/backend contracts and Tunnel platform enum.
- Existing Linux Remote daemon/private IPC integration test.
- Existing SSH exec, PTY, resize, signal, descendant and lifecycle tests.
- Low-memory full Go suite and `-race ./internal/...`, run serially in the
  pinned Go container because this host has no native Go toolchain.
- Linux Qt Release build/CTest and the existing bounded AppImage build or its
  documented prerequisite-equivalent gate, run serially.
- Re-run the Windows-only Task023 contract package cross-build so the extracted
  Linux files do not contaminate the proven Windows probe.

## Acceptance

- No common production file imports Unix-only APIs for the four extracted
  boundaries.
- Linux CLI defaults and error contracts remain compatible; private IPC keeps
  mode/owner/exact-socket cleanup and pre-read peer authentication.
- Linux Terminal and non-PTY Agent exec retain exit status, stdout/stderr,
  resize, signals, cancellation and complete descendant cleanup.
- The Server accepts authenticated Windows hello metadata but still rejects
  every platform outside the strict two-value enum.
- Production Windows backends remain visibly unimplemented rather than being
  represented by a successful no-op.
- Durable context records the user-authorized overlap with Task023 validation
  debt and hands Task025 a concrete backend inventory.

## Resource Gate

All builds and tests are serial or limited to two CPUs with `GOMAXPROCS=2` and
Go package parallelism `-p=1`/`-p=2`. No VM is created and ASD is untouched.
