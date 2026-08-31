---
task_id: task025
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 025 Summary: Windows Core, DPAPI Identity And Private IPC

## Outcome

The first production Windows Core slice is complete and ready for review. The
real `aisummoner-client.exe` now builds on Windows and owns native per-user
runtime policy, a DPAPI-protected Device Identity and authenticated named-pipe
IPC. The existing outbound Tunnel, pairing, status/events and pause/resume
controller remain common with Linux.

This checkpoint does not claim a usable Windows Terminal or Agent. The Windows
SSH backend deliberately rejects both exec and shell requests until Task026
adds PowerShell plus Job Object execution and Task027 adds ConPTY. There is no
success-returning placeholder. Windows also remains an unsupported target until
ordinary-user Windows 11/10, clean-VM GUI and real Tunnel/Terminal/Agent gates
complete; ADR-0007 remains Proposed.

## Files Changed

- `internal/winsecurity/` implements exact process-token facts, ordinary-user
  policy, LocalAppData Known Folder resolution, protected current-user/System
  ACLs, non-reparse checks and DPAPI CurrentUser with UI forbidden.
- `internal/winpipe/` implements the frozen `LOCAL\\AISummoner.Remote.v1`
  carrier with pinned `go-winio`, a protected logon-SID DACL, remote-client
  rejection, first-instance ownership and exact peer PID/token verification
  before protocol bytes are read.
- `internal/clientplatform/platform_windows.go` connects Windows paths,
  privilege policy and shutdown behavior to the production Runtime seam.
- `internal/identity/storage_windows.go` persists a bounded, versioned DPAPI
  envelope with protected ACLs, same-directory flush and atomic no-replace
  commit, stable reload and fail-closed partial/corrupt state handling.
- `internal/clientipc/transport_windows.go` connects the production JSON v1
  dispatcher to the authenticated named-pipe transport.
- `internal/sshserver/process_windows_unsupported.go` lets the production Core
  build while validating Windows cwd syntax and explicitly rejecting exec and
  shell until their native backends exist.
- `internal/windowsprobe/` now delegates security, DPAPI and pipe behavior to
  the production helpers so Qt interoperability cannot drift behind a duplicate
  spike implementation.
- `.github/workflows/windows-remote-contract.yml` builds/tests/vets the real
  Windows Core, runs Qt/MSVC CTest and Qt-to-Go IPC, assembles a checksummed
  engineering ZIP, and independently runs focused Linux normal/race/vet gates.
- `deploy/windows-spike/README.txt` accurately labels the engineering package,
  its real Core, the unavailable Terminal/Agent paths and remaining release
  gates.

## Behavior Changed

### Runtime and identity

- Windows data defaults to
  `%LOCALAPPDATA%\\AISummoner\\RemoteClient` through the Known Folder API.
- Ordinary interactive users and filtered Administrators are accepted;
  elevated/high-integrity tokens, system accounts and Session 0 are rejected.
- The Ed25519 private key is stored only as `device_ed25519.dpapi`, protected by
  DPAPI CurrentUser plus a current-user/System protected DACL. A partial,
  malformed, mismatched or undecryptable identity fails closed rather than
  silently rotating the Device.

### Private IPC

- Qt endpoint `LOCAL\\AISummoner.Remote.v1` maps to native path
  `\\\\.\\pipe\\LOCAL\\AISummoner.Remote.v1`.
- Every accepted pipe verifies the exact client process token's logon SID and
  rechecks the client PID before reading JSON. A second daemon fails rather
  than taking over the endpoint; closing the first listener permits restart.
- The common 64 KiB bounded, strict newline-JSON v1 protocol and controller
  methods are unchanged.

### Shell strategy

- Windows remains native-first. Git Bash/MSYS2 is neither bundled nor required:
  it would not replace token, DPAPI, named-pipe, Job Object or ConPTY work and
  would add a second path/quoting and third-party distribution surface.
- A later explicitly selected `git-bash` Execution Profile may detect an
  existing Git for Windows installation. It must not silently replace the
  native `windows-powershell` profile.

## Verification

### Final native Windows and Linux CI

- Source under test: `065798e68ea3f38913058a573fd75b536027b1ad`
  (`1f67c77` production implementation plus `065798e` Linux CI gate).
- Workflow: [Windows Remote contract run 33359386282](https://github.com/totrytakeoff/AISummoner/actions/runs/33359386282),
  conclusion `success`.
- Windows job `99387667822`: Windows Server 2022, Go 1.23.0 windows/amd64,
  completed in 4m02s.
  - Production `winsecurity`, `winpipe`, `clientplatform`, `identity`,
    `clientipc` and fail-closed `sshserver` native tests: PASS.
  - `windowsprobe` reused the production security/pipe helpers and separately
    kept Task023's PowerShell/Job/ConPTY spike contracts green: PASS in 8.950s.
    Those execution probes are not represented as Task026/027 production code.
  - Production package vet and real `cmd/aisummoner-client` build: PASS.
  - Qt 6.8.3/MSVC Release CTest: 1/1 PASS.
  - Real Qt `QLocalSocket` to Go production named pipe completed
    `status.get` and `events.list`: PASS.
- Linux job `99387667845`: Ubuntu 22.04, Go 1.23.0 linux/amd64, completed in
  2m37s.
  - Focused production Core normal tests: PASS.
  - Race tests for `clientplatform`, `identity`, `clientipc`, `sshserver`,
    `remoteclient` and `tunnel`: PASS.
  - Focused production Core vet: PASS.
- Local low-memory-safe gates: Dockerized `gofmt`, `git diff --check` and
  actionlint 1.7.7: PASS. Heavy local Go builds were intentionally skipped
  because free swap was below the documented 4 GiB resource gate; CI ran the
  equivalent native Windows and Linux gates instead.

The only workflow annotations are non-fatal GitHub-hosted action runtime/cache
warnings. They did not skip a gated command.

### Engineering artifact

- Artifact:
  [AISummoner-Windows-Remote-Engineering-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33359386282/artifacts/9746246672),
  ID `9746246672`, retained through `2026-09-14T05:11:09Z`.
- GitHub artifact wrapper: 67,508,509 bytes, digest
  `sha256:3a7d4924bf86a25d960d0412b5691bde7c55d98d767262ce89677a0cfa2f6a09`.
- Inner engineering ZIP: 67,544,236 bytes, SHA-256
  `7f2046f8f31f0f093e5d62827e082f5706c944c41708ca4b1baa5f97a11fb179`;
  the downloaded file matches the checksum generated on the Windows runner.
- Local ignored copy:
  `dist/task025-windows-ci-065798e/AISummoner-Windows-Remote-Engineering-x86_64.zip`.
- The ZIP contains 58 entries / 117,237,909 expanded bytes, including the real
  16,023,552-byte `aisummoner-client.exe`, Qt GUI, IPC probes, Qt runtime and
  official 25,635,768-byte `vc_redist.x64.exe`.
- Runner facts remain elevated (`SessionID=2`, integrity `0x3000`), so this is
  not ordinary-user desktop launch evidence.

## Deviations From Plan

- No functional deviation. The final Linux regression was moved to a bounded
  GitHub Ubuntu job instead of being forced through the local low-swap host.
- No ASD service or external deployment was touched.

## Known Issues / Follow-Up

- Task026 must replace the explicit exec rejection with native Windows
  PowerShell 5.1, suspended creation, Job Object assignment and joined process
  tree cleanup through the real SSH chain.
- Task027 must replace interactive-shell rejection with ConPTY and prove xterm
  UTF-8, resize, Ctrl-C, disconnect and pause cleanup.
- Task028 still owns ordinary-user GUI auto-launch/no-console-flash, GUI-close
  daemon survival and clean Windows 11/10 engineering-package acceptance.
- Task029 still owns target-aware Server cwd validation, trusted Agent
  Execution Profile, a real DSH Windows Turn, installer/signing and final E2E.
- Different-logon pipe/DPAPI rejection needs a second local Windows account.
- Independent review is not fabricated; this task is handed to the human
  reviewer with ADR-0007 still Proposed.
