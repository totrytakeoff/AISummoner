---
task_id: task025
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 025 Plan: Windows Core, DPAPI Identity And Private IPC

## Status

Ready for implementation. The user explicitly authorized continued native
Windows work after Task024 and set the end goal of a usable Windows Remote.
This task is the first production backend slice; it does not claim Terminal or
Agent execution before Tasks026-029.

## Context

Task023 proved the required Windows APIs and Qt interoperability on a native
Windows Server 2022 runner. Task024 then extracted build-tagged Runtime,
Identity storage, authenticated IPC transport and SSH execution seams while
preserving Linux. Task025 must connect the proven Windows security primitives
to those production seams and make the real Windows Core buildable.

The user proposed bundling Git Bash to reduce PowerShell-specific Agent work.
The product will instead keep native Windows PowerShell as its first execution
contract. Git Bash may later be an explicitly selected, detected capability,
but is not bundled or required here: it does not replace Windows token, DPAPI,
named-pipe, Job Object or ConPTY work and would add a second path/quoting,
update and third-party distribution surface before native correctness exists.

## Goal

Produce a real `aisummoner-client.exe` whose per-user Core can create/reload a
DPAPI-protected Device Identity, connect and pair through the existing outbound
Tunnel, and serve the existing status/events/pause/resume/refresh IPC protocol
over an authenticated Windows named pipe. Unsupported SSH exec and shell must
fail closed until Task026/027 rather than report success.

## Relevant Files

- `internal/clientplatform/`
- `internal/identity/`
- `internal/clientipc/`
- `internal/sshserver/`
- `internal/windowsprobe/`
- new narrowly scoped Windows security/pipe helpers under `internal/`
- `cmd/aisummoner-client/`
- `.github/workflows/windows-remote-contract.yml`
- `deploy/windows-spike/README.txt`
- Windows design, ADR-0007 and durable agent context

## Required Behavior

### Windows Runtime

- Resolve `%LOCALAPPDATA%\\AISummoner\\RemoteClient` through the Known Folder
  API, never cwd or an environment-only fallback.
- Report the strict `windows` hello value.
- Accept ordinary interactive users, including filtered Administrators, while
  rejecting elevated/high-integrity tokens, service accounts and Session 0.
- Treat the Linux-only privileged development override as unavailable on
  Windows and handle process interruption without Unix signal imports.
- Validate bounded absolute Windows data directories without assuming a Unix
  socket lives inside them.

### Device Identity

- Preserve common Ed25519, Device ID, challenge-signing and SSH-host-key
  semantics.
- Store PKCS#8 only inside a versioned DPAPI CurrentUser envelope with UI
  forbidden and application entropy.
- Protect the non-reparse data directory and identity/metadata files with a
  protected current-user plus LocalSystem DACL.
- Use same-directory temporary files, flush and an atomic no-replace commit so
  two starting daemons cannot silently rotate the Device key.
- Bound stored input and fail closed for missing-key/metadata partial state,
  malformed envelope, DPAPI failure, wrong key type or metadata mismatch.

### Private IPC

- Freeze Qt endpoint `LOCAL\\AISummoner.Remote.v1` and native path
  `\\\\.\\pipe\\LOCAL\\AISummoner.Remote.v1`; accept only the bounded frozen
  namespace grammar.
- Use pinned `go-winio v0.6.2` byte-mode pipes, protected logon-SID DACL,
  remote-client rejection and first-instance ownership.
- Authenticate every accepted exact pipe handle before protocol bytes are read
  by comparing the peer process token's `TokenLogonSid`, then re-read its PID.
- Preserve common JSON v1 framing, 64 KiB bound, strict decoding, deadlines,
  eight-handler admission and controller error mapping.
- A second daemon must fail deterministically; closing the listener must allow
  a later restart.

### Fail-closed SSH boundary

- Provide a real Windows backend constructor so the production Core builds.
- Recognize and validate Windows absolute cwd syntax locally.
- Reject exec, interactive shell, resize and signal behavior until the real
  PowerShell/Job and ConPTY backends land; no request may be reported as
  successful.

### Engineering CI artifact

- Native Windows CI runs the production backend tests and builds the real Core
  with `CGO_ENABLED=0` and bounded package parallelism.
- The Qt-to-Go probe must exercise the same production pipe/security helper,
  not a drifting duplicate.
- Bundle the real `aisummoner-client.exe` beside the Qt GUI in an explicitly
  unsigned engineering ZIP with SHA-256 and an accurate limitations README.

## Verification

- Windows cross-build and vet for the production client and Windows-only
  packages in the pinned Go container.
- Native Windows tests for token policy, DPAPI round trip/corruption, protected
  ACL, stable identity reload/partial-state rejection, pipe grammar,
  authentication, concurrency, second-instance and restart.
- Native Windows production Core build plus Qt/MSVC CTest and Qt-to-Go pipe
  interoperability through GitHub Actions.
- Focused Linux tests and race tests for every common package touched.
- Linux Qt CTest and AppImage regression if shared GUI/package files change.
- Repository-wide Go/Web gates are recorded honestly; the pre-existing DSH
  40 ms timing fixture is not silently reclassified.

## Documentation Requirements

- Record the native-shell-first decision and optional future Git Bash profile.
- Update Task025 summary with exact commands, native run URL/job, artifact hash
  and any unverified ordinary-user/clean-VM gates.
- Keep ADR-0007 Proposed until its real Windows 11/10 acceptance evidence is
  complete.

## Out Of Scope

- PowerShell/Job Object SSH exec (Task026).
- ConPTY Terminal (Task027).
- Final Qt launcher polish, clean-VM ZIP acceptance or installer (Task028).
- Server target-aware cwd, DSH Execution Profile and real Windows Agent Turn
  (Task029).
- Bundled Git Bash/MSYS2, WSL, `pwsh`, macOS, Windows Service, MSIX or signing.
- ASD deployment, Server schema/protocol changes or Browser feature work.

## Acceptance Criteria

The task is ready for review when:

- Production Windows Core cross-builds with only real or explicitly rejecting
  backends; there is no success-returning placeholder.
- Native Windows tests prove Runtime, DPAPI/ACL Identity and authenticated
  named-pipe behavior through the production seams.
- The real Core and Qt GUI are present in a checksummed unsigned engineering
  ZIP, while Terminal/Agent limitations remain prominent.
- Linux Remote behavior and shared protocol/security gates have no regression.
- Durable context identifies Task026 as the next implementation slice and does
  not claim Windows support or accept ADR-0007.

## Resource Gate

Local builds/tests use at most two CPUs, `GOMAXPROCS=2` and Go `-p=1`/`-p=2`.
Heavy local work starts only with at least 8 GiB available memory and 4 GiB
free swap. No VM image is created, and ASD remains untouched.
