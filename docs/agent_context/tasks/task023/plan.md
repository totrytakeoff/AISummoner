---
task_id: task023
type: plan
status: in_progress
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 023 Plan: Windows Remote Compatibility And Security Spike

## Status

Implementation authorized by the user on 2026-08-31. This task remains limited
to bounded Windows API, toolchain and interoperability proofs. It does not
enable `platform=windows` on the production Tunnel, deploy a Windows client, or
modify the accepted Linux runtime behavior.

## Goal

Replace the remaining high-risk assumptions in Proposed ADR-0007 with evidence
from Windows 11 and, where the API behavior may differ, Windows 10 22H2. Freeze
the exact local IPC carrier/endpoint, process creation and cleanup sequence,
identity protection format, Qt daemon launcher and build toolchain before the
production port is split across Tasks024-029.

## Owner

Primary implementation agent. No delegated coding agent is planned, preserving
the user's existing solo-refactor direction.

## Inputs

- `docs/design/windows-remote-client-port.md`
- `docs/decisions/ADR-0007-windows-remote-client-platform.md`
- Task015 private IPC and Task016 Qt GUI contracts
- Current `golang.org/x/sys/windows`; a pinned Microsoft `go-winio` candidate
- Official Microsoft ConPTY, Job Object, named-pipe, token and DPAPI contracts
- Official Qt `QLocalSocket`, Windows/MSVC and `windeployqt` contracts

## Required Proofs

### 1. Build inventory

- Run a bounded `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` client build to record
  every current compile boundary rather than treating the known source audit as
  exhaustive.
- Configure/build the Qt Widgets target with MSVC 2022 x64 and the pinned Qt
  version; record required CMake/resource/manifest changes.
- Establish a serial Windows CI job with artifact hashes and no signing secret.

### 2. Qt-to-Go local IPC

- Build a throwaway or test-only Go named-pipe server and Qt `QLocalSocket`
  client using the existing newline JSON framing.
- Freeze the exact Go full pipe path and Qt server name.
- Prove 64 KiB bounds, deadlines, disconnect/restart and concurrent requests.
- Set a protected logon-SID DACL, reject remote clients and prove a different
  token/logon cannot read status.
- After accept, obtain the peer token from the exact pipe instance and compare
  `TokenLogonSid`; reject before reading any request.
- Prove exclusive first-instance behavior and automatic recovery after a hard
  server exit.

The spike must decide whether pinned `go-winio` safely supports peer handle
inspection. If not, it must recommend a narrow private listener; DACL-only is
not an acceptable fallback.

### 3. Privilege and identity

- Distinguish non-elevated administrator membership from an elevated token;
  accept the former and reject the latter, high integrity, system accounts and
  Session 0.
- Resolve the exact `%LOCALAPPDATA%\AISummoner\RemoteClient` root from both Go
  and Qt without cwd/environment-only assumptions.
- Round-trip a versioned Ed25519 PKCS#8 blob through DPAPI CurrentUser with UI
  forbidden, protected ACLs and an atomic write.
- Prove wrong user/corruption fails closed and does not replace metadata or
  silently create a new Device.

### 4. Process tree and non-PTY exec

- Create a kill-on-close Job Object, create PowerShell suspended, assign it,
  then resume it.
- Prove robust command transport, Windows cwd, Chinese UTF-8 stdout/stderr,
  non-zero exit status and bounded cancellation.
- Spawn a grandchild process, cancel/close the session, and prove no descendant
  remains after the joined deadline.
- Record behavior when the test runner is already inside another Job Object.

### 5. ConPTY

- Create/resize/close ConPTY using `x/sys/windows` and a suspended child assigned
  to the same Job discipline.
- Prove Windows PowerShell prompt, VT/UTF-8, input, resize and Ctrl-C behavior.
- Service input/output on independent workers and prove full-buffer teardown
  does not deadlock.
- Repeatedly connect/disconnect and assert no process, goroutine or native
  handle growth beyond the bounded baseline.

### 6. Qt daemon launch and packaging probe

- Prove the GUI can launch the console-subsystem Core detached without a visible
  console and that GUI exit leaves it alive.
- Decide whether configured `QProcess` is sufficient or a tiny native launcher
  seam is required.
- Run `windeployqt`, copy the result to a clean VM without Qt, and start it.
- Record the exact Qt/MSVC runtime and minimum Windows versions. Installer and
  Authenticode work remain out of this spike.

## Deliverables

- Reproducible test/probe sources kept only if they are useful as future
  contract tests; no production stub is retained merely to satisfy compilation.
- A results document under `docs/agent_context/tasks/task023/summary.md` with
  OS builds, tool versions, hashes, commands, failures/retries and handle/
  process cleanup evidence.
- Proposed ADR-0007 revised to the proven carrier, endpoint, shell encoding,
  launcher and support baseline, then either marked Accepted or returned to
  architecture review.
- A narrow Task024 plan for platform extraction. No later task is authorized by
  implication.

## Non-Goals

- No public Windows binary or GitHub Release.
- No production Server hello change or public deployment.
- No Windows Qt UI redesign.
- No DSH/OpenCode/Codex/Claude implementation change.
- No Windows Service, MSIX, auto-update, ARM64, Desktop control or filesystem
  capability.
- No weakening of peer checks, non-elevated execution or joined cleanup to make
  a probe pass.

## Acceptance

- Every Required Proof has executable Windows evidence, or the ADR explicitly
  records a blocker and safer replacement.
- Linux worktree behavior remains unchanged and its existing tests/builds pass
  after any reusable test seam is added.
- A different local logon cannot use the pipe or decrypt the identity blob.
- A canceled exec/PTY leaves no proven descendant or open session handles.
- Qt starts the Core with no console flash, reconnects over the frozen pipe name
  and can exit without stopping it.
- The result is independently reviewable; no acceptance is inferred from a
  compile-only or same-process fake.

## Resource Gate

All Go/Qt work is serial. Windows CI uses one architecture and one build at a
time; no parallel VM matrix is started during ConPTY stress. The existing ASD
Server and unrelated services are not modified by this spike.
