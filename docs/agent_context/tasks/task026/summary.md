---
task_id: task026
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 026 Summary: Windows PowerShell Exec And Job Object Lifecycle

## Outcome

The production Windows Remote Core now supports non-interactive SSH `exec`
through inbox Windows PowerShell 5.1 and a kill-on-close Job Object. The real
native path has passed TLS/WSS, Device challenge, yamux, strict Device SSH host
key verification and the production embedded SSH server on Windows Server
2022. UTF-8 stdout/stderr, Windows cwd, exit status, high-volume drain, signal
reply semantics, cancellation, normal root exit and Tunnel shutdown are covered.

This checkpoint does not claim interactive Terminal or Windows Agent support.
Windows `shell` still rejects explicitly until Task027 adds ConPTY, and the
Server-owned target path/Execution Profile plus real DSH PowerShell Turn remain
Task029. The artifact is unsigned and its hosted runner was elevated; ordinary-
user Windows 11/10 and clean-VM acceptance remain open. ADR-0007 stays Proposed.

## Files Changed

- `internal/winprocess/process_windows.go` adds the shared production Windows
  primitives for the fixed PowerShell path, valid-UTF-8 to UTF-16LE encoded
  command transport, Profile Known Folder/default cwd, kill-on-close Job
  creation, suspended create/assign/resume and native waiting.
- `internal/winprocess/process_windows_test.go` covers command encoding,
  invalid UTF-8, path selection and working-directory contracts.
- `internal/sshserver/process_windows.go` replaces the Task025 exec rejection
  with the production non-PTY backend, explicit inherited standard-handle list,
  concurrent stream pumps, Job-wide termination and joined cleanup. Interactive
  shell remains fail closed.
- `internal/sshserver/process_windows_test.go` covers backend path behavior,
  shell rejection, invalid input and repeated native-handle cleanup.
- `internal/windowsprobe/process_windows.go` now reuses `internal/winprocess`
  rather than keeping a second Job/PowerShell implementation. Its ConPTY probe
  remains separate pending Task027 production work.
- `internal/sshclient/windows_integration_test.go` proves the real Windows
  Device challenge, TLS/WSS, yamux, strict SSH and production SSHD chain,
  including process-tree lifecycle and exact SSH signal replies.
- `internal/sshclient/client_test.go` and
  `internal/sshclient/tunnel_integration_test.go` are correctly Linux-tagged
  because their fixtures invoke Unix commands and syscalls.
- `.github/workflows/windows-remote-contract.yml` gates the new packages and
  integration path, vets/builds the production Core and triggers for SSH client
  changes while retaining bounded Linux regression and Qt packaging jobs.
- `deploy/windows-spike/README.txt` identifies working non-PTY PowerShell exec
  and the still-unavailable ConPTY/Agent/release gates.

## Behavior Changed

### PowerShell and cwd

- Exec resolves only the system Windows PowerShell 5.1 executable. It does not
  search `PATH`, select `pwsh`, fall back to `cmd.exe`, or detect Git Bash/WSL.
- A valid UTF-8 SSH command is prefixed with the frozen UTF-8 console/output
  setup, encoded as UTF-16LE Base64 and passed through `-EncodedCommand` with
  `-NoLogo -NoProfile -NonInteractive`. Invalid UTF-8 rejects before launch.
- Omitted cwd resolves to the current user's Profile Known Folder. Supplied cwd
  must be a clean, bounded, existing absolute Windows directory.
- stdin is independent from concurrently drained stdout and stderr. The SSH
  exit status is the root PowerShell exit code; non-zero status is not a
  transport failure.

### Process-tree and signal lifecycle

- Each exec creates a new kill-on-close Job before launching PowerShell
  suspended. The child is assigned before its primary thread resumes, so it
  cannot create a descendant outside the Job during the assignment window.
- Normal root exit first terminates any remaining descendants, then joins input
  and both output pumps. Context cancellation, channel close and Tunnel shutdown
  terminate the whole Job and close all owned handles before completion.
- Non-PTY `TERM` and `KILL` accept the SSH request and terminate the Job with
  the frozen 143/137 status. `INT` rejects its request instead of pretending to
  provide POSIX control-event semantics.
- Interactive `shell` remains an explicit error. No no-op path reports Terminal
  success before ConPTY lands.

### Shell strategy

Windows stays native-first. Git Bash/MSYS2 is neither bundled nor required and
does not hide the target OS. A future installed-Git integration may exist only
as an explicitly selected `git-bash` Execution Profile; the mandatory fallback
remains `windows-powershell` plus ConPTY.

## Verification

### Final native Windows and Linux CI

- Source under test: `4162fa075a441ff4508b916e81c6a3a4eabaad5c`
  (`5424420` implementation, `eb2e39d` signal-reply test correction and
  `4162fa0` complete workflow path trigger).
- Workflow: [Windows Remote contract run 33361499043](https://github.com/totrytakeoff/AISummoner/actions/runs/33361499043),
  conclusion `success`.
- Windows job `99393598827`: Windows Server 2022 build `10.0.20348`, Go 1.23.0
  windows/amd64, completed in 3m56s.
  - `winsecurity`, `winpipe`, `winprocess`, `clientplatform`, `identity` and
    `clientipc`: PASS.
  - Production `sshserver` tests, including repeated real PowerShell/native
    handle cleanup: PASS in 4.125s.
  - `TestWindowsTunnelSSHPowerShellEndToEnd`: PASS in 1.91s. The one native
    test owns a real TLS challenge endpoint, WSS Tunnel, yamux manager, strict
    SSH client and production embedded SSHD. It covers Chinese UTF-8 stdout and
    stderr, cwd, non-zero exit, bounded high-volume output, fail-closed shell,
    explicit INT/TERM/KILL replies, context cancellation, normal leader exit
    and Tunnel shutdown. Each cleanup case proves its descendant is gone while
    an unrelated sentinel survives; cancellation also proves the Tunnel remains
    usable.
  - Task023 probe after production primitive reuse: PASS in 5.222s, including
    its separate ConPTY spike. That spike is not represented as Task027
    production Terminal support.
  - Package vet, real `aisummoner-client.exe` build, Qt 6.8.3/MSVC Release
    CTest (1/1) and Qt `QLocalSocket` `status.get`/`events.list` interoperability:
    PASS.
- Linux job `99393598721`: Ubuntu 22.04, Go 1.23.0 linux/amd64, completed in
  44s. Focused common/Core normal tests, race tests and vet all passed.
- Low-memory-safe local checks: capped Docker `gofmt`, actionlint 1.7.7,
  `git diff --check` and repository status checks: PASS. Heavy local Go/race/Qt
  work was intentionally not run because free swap was below the documented
  4 GiB gate; the bounded GitHub jobs supplied those gates. ASD was untouched.

Both final jobs emitted non-fatal GitHub action Node-runtime and setup-go cache
restore/save warnings. All gated test/build/package commands still ran and
passed.

### Failure and retry evidence

- Initial workflow [run 33361261244](https://github.com/totrytakeoff/AISummoner/actions/runs/33361261244)
  failed only its Windows job `99392916782`; the Linux job passed. Production
  SSH server signal mapping and repeated handle cleanup had already passed, but
  the full-chain test used `ssh.Session.Signal`, whose request does not wait for
  a server reply, and incorrectly interpreted a nil send result as server
  acceptance of `INT`.
- Commit `eb2e39d` changed the test to send the raw SSH `signal` request with
  `want-reply=true`, so rejection/acceptance is asserted from the server reply;
  it did not weaken production behavior. Commit `4162fa0` then added
  `internal/sshclient/**` to the workflow paths because the test-only push had
  correctly produced no workflow run under the previous trigger set.
- Final run `33361499043` passed the corrected INT rejection and TERM/KILL
  acceptance assertions as part of the complete native chain.

### Engineering artifact

- Artifact:
  [AISummoner-Windows-Remote-Engineering-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33361499043/artifacts/9746897971),
  ID `9746897971`, retained through `2026-09-14T05:46:29Z`.
- GitHub artifact wrapper: 67,543,502 bytes, digest
  `sha256:6f3d961434cb820865e68f2ad5d46ca3d24bfdcc9be37202a05d3d9b637a55a6`.
- Inner engineering ZIP: 67,580,489 bytes, SHA-256
  `1dfc76ebec8fd81eb6d0811cccc26ad54c8518a98156d487f39712a5f7c3b9da`;
  `sha256sum -c` passed against the checksum created on the Windows runner.
- Local ignored copy:
  `dist/task026-windows-ci-4162fa0/AISummoner-Windows-Remote-Engineering-x86_64.zip`.
- The ZIP contains 58 files / 117,302,654 expanded bytes, including the real
  16,088,064-byte `aisummoner-client.exe`, Qt GUI, IPC/contract probes, Qt
  runtime and the official 25,635,768-byte Visual C++ Redistributable.
- Runner facts report `SessionID=2`, high integrity `0x3000` and `Elevated=true`.
  Therefore this package is engineering evidence, not ordinary-user desktop or
  release acceptance.

## Deviations From Plan

- No functional scope deviation. Native compilation and execution ran in the
  bounded GitHub Windows job instead of the low-swap local Linux host.
- No ASD deployment, public release, installer, signing or external server
  change was performed.

## Known Issues / Follow-Up

- Task027 must replace fail-closed interactive shell with production ConPTY and
  prove VT/UTF-8, resize, Ctrl-C, disconnect and pause/Tunnel cleanup through
  the same real chain.
- Task028 still owns ordinary-user GUI launch of the sibling Core without a
  console flash, GUI-close daemon survival and clean Windows 11/10 engineering-
  package acceptance.
- Task029 still owns Linux-Server target-aware Windows cwd, the trusted
  `windows-powershell` Agent Execution Profile, a real DSH Turn, installer,
  signing and final public Server/Controller E2E.
- Different-logon pipe/DPAPI rejection needs a second local Windows account.
- Task025 and Task026 remain independent-review debt. This coder handoff does
  not provide a review verdict or accept ADR-0007.
