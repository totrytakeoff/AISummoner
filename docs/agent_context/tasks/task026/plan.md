---
task_id: task026
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 026 Plan: Windows PowerShell Exec And Job Object Lifecycle

## Status

Ready for implementation under the active user goal to deliver a native
Windows Remote Client. Task025 remains awaiting independent review; advancing
this narrow execution slice does not fabricate an approval or accept Proposed
ADR-0007.

## Owner

Primary implementation agent, working without delegated coding agents as the
user previously requested.

## Reviewer

Human reviewer. No independent verdict is inferred from CI or from continued
implementation authorization.

## Context

Task023 proved Windows PowerShell 5.1, suspended process creation, Job Object
descendant cleanup and UTF-8 I/O in a Windows-only probe. Task024 extracted the
production SSH execution seam, and Task025 connected the real Windows Core
while deliberately rejecting both exec and shell. Task026 must replace only
the exec rejection with production code and prove it through the existing
TLS/WSS, yamux and strict SSH chain. Interactive shell remains rejected until
Task027 supplies ConPTY.

Git Bash/MSYS2 remains optional future profile work. It is not bundled or used
as a fallback in this task; Windows PowerShell 5.1 is the frozen native exec
contract.

## Goal

Allow the production Windows Remote Core to execute one bounded SSH `exec`
request as the current ordinary desktop user through Windows PowerShell 5.1,
with separate UTF-8 stdout/stderr, exact exit status, validated cwd and joined
cleanup of the complete child process tree on normal parent exit, cancellation,
SSH disconnect, Tunnel shutdown, TERM or KILL.

## Relevant Files

- new `internal/winprocess/` production Windows process primitives
- `internal/sshserver/process_windows_unsupported.go`
- `internal/sshserver/process_windows_unsupported_test.go`
- `internal/windowsprobe/process_windows.go`
- `internal/windowsprobe/contracts_windows_test.go`
- `internal/sshclient/client_test.go`
- `internal/sshclient/tunnel_integration_test.go`
- new Windows-native SSH/Tunnel integration tests
- `.github/workflows/windows-remote-contract.yml`
- `deploy/windows-spike/README.txt`
- Windows design, ADR-0007 and durable agent context

## Required Behavior

### PowerShell command contract

- Resolve only the system Windows PowerShell 5.1 executable; do not search
  `PATH`, auto-select `pwsh`, Git Bash, WSL or `cmd.exe`.
- Pass the exact valid UTF-8 SSH command through UTF-16LE `-EncodedCommand`,
  prepended only with the frozen UTF-8 console/output setup. Never construct a
  quoting-sensitive `-Command` string.
- Preserve the common 1–8192-byte and NUL bounds; reject invalid UTF-8 instead
  of replacing bytes during UTF-16 conversion.
- Run with `-NoLogo -NoProfile -NonInteractive` and no visible console window.
- Connect SSH stdin and separate stdout/stderr through an explicit inherited
  handle list. Drain both outputs concurrently and completely even when the
  Server capture limit truncates its retained copy.
- Return the PowerShell process exit code through the existing SSH
  `exit-status` request; non-zero exit remains a valid transport result.

### Working directory

- An omitted cwd resolves through the current user's Profile Known Folder, not
  the daemon/install/current directory.
- A supplied cwd must be a clean, bounded absolute Windows directory with no
  NUL and must exist at launch time.
- Do not add Server target-aware path syntax here; Task029 owns the Linux
  Server accepting `C:\\...` from Browser/Agent. Native Windows tests may call
  the common SSH client in a Windows process where `filepath` has Windows
  semantics.

### Job and handle lifecycle

- Create a fresh Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` before
  the child.
- Create PowerShell suspended with only the intended standard handles,
  successfully assign it to the Job, and only then resume its primary thread.
- Failure at any creation/assignment/resume step terminates and waits the child
  where applicable, closes every pipe/native handle and reports launch failure.
- Root exit terminates the remaining Job before output drains join, preventing
  an inherited pipe handle in a background descendant from hanging the SSH
  result.
- Context cancellation, SSH channel close and Tunnel shutdown terminate the
  Job, join the process/output/input workers and close all handles before the
  handler reports completion.
- SSH `TERM` and `KILL` terminate the whole Job. Non-PTY `INT` is explicitly
  rejected until a reliable Windows control-event contract is separately
  proven; it must not be reported as accepted.
- Finalization and late signal/terminate calls are race-safe and idempotent.

### Preserved fail-closed boundary

- `pty-req` may still be parsed by the common SSH layer, but Windows `shell`,
  resize and interactive Ctrl-C remain rejected by the platform backend.
- No code path reports Terminal or Windows Agent profile support in this task.

### Production reuse

- Move the already-proved PowerShell path, UTF-16LE encoding, working-directory,
  kill-on-close Job, suspended assignment and native wait primitives into a
  narrowly scoped production package used by both `sshserver` and the Task023
  probe.
- Do not retain two independently evolving security/lifecycle implementations.

## Verification

- Native Windows unit/contract tests for clean/default/missing cwd, command
  UTF-8/encoding, stdout/stderr/exit, background-parent cleanup, cancellation,
  TERM/KILL, INT rejection, shell rejection and repeat handle cleanup.
- Native Windows full-chain test using an `httptest` TLS server and the real
  Device challenge, WSS, yamux Manager, strict Device SSH host-key validation,
  production Embedded SSHD and `sshclient.Dialer`.
- Full-chain cancellation and Tunnel shutdown must prove a spawned descendant
  PID is no longer running while the Device Tunnel remains healthy after a
  single exec cancellation.
- Windows `go test`, `go vet` and production Core build remain bounded by
  `GOMAXPROCS=2`, `GOFLAGS=-p=1` and the workflow timeout.
- Focused Linux normal/race/vet regression covers every common package touched.
- Qt/MSVC CTest, Qt-to-Go IPC and engineering ZIP assembly remain green; the
  bundled README must distinguish working non-PTY exec from unavailable
  Terminal/Agent-profile support.

## Documentation Requirements

- Record exact native run/job/artifact hashes and any failure/retry evidence.
- Update the Windows design and ADR-0007 evidence without changing the ADR
  from Proposed.
- Identify Task027 ConPTY as the next implementation slice and keep Task029's
  Server-owned Execution Profile/cwd work explicit.

## Out Of Scope

- ConPTY, interactive PowerShell, PTY resize or Ctrl-C (Task027).
- Qt ordinary-user auto-launch/clean-VM acceptance or installer (Task028).
- Linux-Server target-aware Windows cwd, DSH Execution Profile, real Agent Turn,
  signing or public Windows support (Task029).
- Git Bash/MSYS2/WSL/pwsh bundling or detection.
- Windows Service, LocalSystem, elevation, sandbox claims or arbitrary command
  policy.
- Server schema/API changes, Controller UI changes or ASD deployment.

## Acceptance Criteria

The task is ready for review when:

- Production Windows SSH exec succeeds through the real native backend and the
  full TLS/WSS/yamux/strict-SSH test, with UTF-8 streams, cwd and exit status.
- Normal root exit, context cancel and Tunnel shutdown all prove Job-owned
  descendants are gone before completion; no output drain hangs.
- TERM/KILL and INT behavior match the frozen contract.
- Interactive shell still fails closed and the package/docs make that boundary
  unmistakable.
- The Task023 probe delegates the critical process primitives to production
  code, Windows/Linux gates are green and the unsigned artifact contains the
  new real Core with an exact checksum.
- No Windows support claim, ADR acceptance or independent review is fabricated.

## Resource Gate

Local heavy builds/tests require at least 8 GiB MemAvailable and 4 GiB free
swap and use at most two CPUs. Current free swap is below that gate, so native
and Linux compilation/race execution must run in bounded GitHub jobs; local
work is limited to formatting, static diff checks and other memory-capped
operations. ASD remains untouched.
