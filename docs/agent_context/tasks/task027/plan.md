---
task_id: task027
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 027 Plan: Windows ConPTY Terminal And Joined Lifecycle

## Status

Ready for implementation under the active user goal to deliver a native
Windows Remote Client. Tasks025-026 remain awaiting independent review;
advancing this narrow Terminal slice does not fabricate either approval or
accept Proposed ADR-0007.

## Owner

Primary implementation agent, working without delegated coding agents as the
user previously requested.

## Reviewer

Human reviewer. CI and continued implementation authorization are evidence,
not an independent verdict.

## Context

Task023 proved a Windows PowerShell 5.1 ConPTY spike with UTF-8, resize, Ctrl-C
and repeated cleanup. Task024 extracted the production SSH execution seam,
Task025 connected the real Windows Core, and Task026 implemented non-PTY
PowerShell/Job execution through the native TLS/WSS/yamux/strict-SSH chain.
Production `shell` still rejects explicitly. Task027 must replace only that
rejection with a joined ConPTY backend while preserving Task026 exec behavior.

Git Bash/MSYS2 remains an optional future Execution Profile. It is not bundled,
detected or used as a fallback; the fixed native Terminal is inbox Windows
PowerShell 5.1 over ConPTY.

## Goal

Allow the production Windows Remote Core to serve one interactive SSH PTY as
the current ordinary desktop user, carrying PowerShell VT/UTF-8 bytes, resize
and Ctrl-C through the existing Browser Terminal path while ensuring normal
exit, channel/context cancellation, disconnect, Tunnel shutdown, TERM/KILL and
Core pause join the complete Job, ConPTY, pipe, process and worker lifecycle.

## Relevant Files

- new shared ConPTY primitives under `internal/winprocess/`
- `internal/sshserver/process_windows.go`
- `internal/sshserver/process_windows_test.go`
- `internal/windowsprobe/process_windows.go`
- `internal/windowsprobe/contracts_windows_test.go`
- `internal/sshclient/windows_integration_test.go`
- `.github/workflows/windows-remote-contract.yml`
- `deploy/windows-spike/README.txt`
- Windows design, ADR-0007 and durable agent context

## Required Behavior

### Native ConPTY launch

- Resolve only system Windows PowerShell 5.1 and use the same frozen UTF-8
  prefix/cwd policy as Task026; do not search `PATH`, select `pwsh`, Git Bash,
  WSL or `cmd.exe`.
- Create synchronous input/output pipe pairs, then `CreatePseudoConsole` with
  the requested bounded dimensions.
- Attach only `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`. Explicitly publish null
  standard handles so a console-attached parent cannot donate the CI/launcher
  console and bypass ConPTY.
- Create a kill-on-close Job before PowerShell, launch suspended, assign before
  resume and return no partially initialized process on any failure.
- Move the proven ConPTY creation primitive into `internal/winprocess` so the
  production SSH backend and Task023 probe cannot drift into two launch paths.

### SSH Terminal contract

- Preserve common `pty-req`, `shell`, `window-change`, CWD environment and
  bounds. The requested terminal name remains protocol metadata; PowerShell is
  fixed by the trusted platform backend.
- Forward SSH channel input byte-for-byte to ConPTY and its single combined
  VT/UTF-8 output stream back to SSH stdout. Do not invent a PTY stderr stream.
- `window-change` calls `ResizePseudoConsole` under a close-safe native-handle
  lifecycle and returns the actual SSH reply.
- Ctrl-C from Browser/xterm remains terminal input byte `0x03`. A PTY SSH
  `signal INT` request may map to the same byte and must report the real write
  result. TERM/KILL keep Task026's explicit Job-wide 143/137 contract.
- Natural PowerShell exit returns its status and sends the existing SSH
  `exit-status`; non-zero remains a terminal process result rather than a
  transport downgrade.

### Joined cleanup

- Service input and output concurrently; no synchronous read/write ordering may
  deadlock a full ConPTY pipe.
- On normal root exit, terminate remaining Job descendants, stop input, close
  the pseudoconsole in a resize-safe order, drain terminal tail output and only
  then publish completion/close native handles.
- Channel close, Browser disconnect, context cancellation, Tunnel shutdown and
  Core pause terminate the complete Job, close/unblock ConPTY I/O and join all
  workers. No descendant or pump may survive the session.
- Resize, INT, terminate and finish must be race-safe and idempotent; no late
  call may use a closed/recycled native handle.
- Closing one PTY must not terminate an unrelated process or the Device Tunnel.

## Verification

- Native Windows unit tests for invalid launch input, fixed cwd/path behavior,
  real PowerShell shell launch, repeated handle cleanup, resize/close races and
  late-operation rejection.
- Extend the native full-chain test to open the production PTY over the real
  Device challenge, TLS/WSS, yamux, strict Device SSH key and embedded SSHD.
- The full-chain proof must observe Chinese UTF-8, PowerShell VT/prompt behavior,
  cwd, `101x37` resize, Ctrl-C interruption and a subsequent successful command.
- Native lifecycle tests must prove descendants are gone after normal root
  exit, PTY close/context cancel and Tunnel shutdown while an unrelated
  sentinel survives; canceling one PTY must leave the Device Tunnel usable.
- Preserve Task026 non-PTY stdout/stderr/cwd/exit/signal/descendant tests.
- Windows `go test`, `go vet`, production Core build, Qt CTest/IPC and unsigned
  engineering ZIP run with the existing bounded workflow. Focused Linux
  normal/race/vet protects common SSH/Terminal behavior.
- Local work stays limited to formatting/static checks because free swap is
  below the documented heavy-build resource gate. ASD remains untouched.

## Documentation Requirements

- Record exact native run/job/artifact hashes and every failure/retry.
- Update the Windows design and ADR-0007 evidence without changing Proposed.
- Identify Task028 ordinary-user Qt launch/clean-VM packaging as the next slice
  and keep Task029 Server-owned Execution Profile/real DSH proof explicit.

## Out Of Scope

- Qt ordinary-user sibling-Core launch, no-console-flash or clean-VM acceptance
  (Task028).
- Linux-Server target-aware Windows cwd, trusted Agent Execution Profile, real
  DSH PowerShell Turn, installer, signing or public Windows support (Task029).
- Git Bash/MSYS2/WSL/pwsh bundling, detection or automatic shell selection.
- Windows Service/LocalSystem, elevation, desktop control, arbitrary local
  listeners or Server/Controller feature redesign.

## Acceptance Criteria

The task is ready for review when:

- Production Windows `OpenPTY` succeeds through the real native chain and
  proves UTF-8/VT, cwd, resize, Ctrl-C and continued interaction.
- Normal exit, close/cancel and Tunnel shutdown prove Job descendants and all
  session workers/handles are gone without harming an unrelated sentinel.
- Resize/signal/finish races fail closed, Task026 exec remains green and the
  Task023 probe reuses production launch primitives.
- Linux common regressions, Qt IPC/build and the checksummed unsigned Windows
  artifact pass, with Terminal now accurately labeled available but Agent and
  release support still unavailable.
- No Windows support claim, ADR acceptance or independent review is fabricated.

## Resource Gate

Local heavy builds/tests require at least 8 GiB MemAvailable and 4 GiB free
swap with at most two CPUs. Current free swap is below that gate, so native and
Linux compile/race execution must run in bounded GitHub jobs; local operations
remain memory-capped. ASD is not used.
