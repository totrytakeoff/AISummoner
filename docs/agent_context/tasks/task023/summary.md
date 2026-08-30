---
task_id: task023
type: summary
status: in_progress
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 023 Summary: Windows Remote Compatibility And Security Spike

## Current Outcome

The first Windows implementation slice is complete and reproducible. A native
Windows Server 2022 GitHub Actions run now passes the Go security/runtime
contracts, Qt 6.8.3 MSVC build and CTest suite, real Qt `QLocalSocket` to Go
named-pipe exchange, and `windeployqt` assembly. The reusable choices required
for the production port are implemented behind Windows build tags and Qt
platform guards.

Task023 deliberately remains `in_progress`. The generated ZIP is an unsigned
compatibility artifact, not a usable Windows Remote Client: the production Go
Core still contains Linux-only `sshserver` code, and the clean non-elevated
Windows 11/10 VM, different-logon rejection, detached Core/no-console-flash and
real Tunnel/Terminal checks have not yet run. The user explicitly allowed
GitHub Actions packaging and said this validation need not block the current
implementation pass.

## Implemented Contracts

### Windows Go proofs

- `internal/windowsprobe` resolves
  `%LOCALAPPDATA%\AISummoner\RemoteClient` through the Known Folder API and
  distinguishes a filtered administrator token from elevated/high-integrity,
  system-account and Session 0 tokens.
- Device Identity material can be sealed with DPAPI CurrentUser and UI
  forbidden, stored in a versioned envelope, protected by a non-inheriting
  current-user/SYSTEM ACL, flushed and atomically replaced. Corruption fails
  closed.
- The frozen local IPC names are
  `LOCAL\AISummoner.Remote.v1` for Qt and
  `\\.\pipe\LOCAL\AISummoner.Remote.v1` for Go. The pinned
  `github.com/Microsoft/go-winio v0.6.2` listener uses a protected logon-SID
  DACL, rejects remote clients and enforces first-listener ownership.
- Each accepted pipe is authenticated before any protocol read by resolving
  the exact client PID, opening its process token, comparing `TokenLogonSid`,
  and re-reading the exact pipe client PID to fail closed on a peer change.
  `ImpersonateNamedPipeClient` was rejected because it races with Qt's
  connect-before-first-write behavior.
- Non-PTY Windows PowerShell uses UTF-16LE `-EncodedCommand`, explicit cwd and
  bounded UTF-8 stdout/stderr drains. The root process is created suspended,
  assigned to a kill-on-close Job Object, then resumed; cancellation proves a
  spawned descendant is gone.
- ConPTY uses synchronous pipes, an explicit pseudoconsole process attribute,
  independent input/output workers, null inherited standard handles, Job
  ownership, resize, Ctrl-C, joined close and repeated native-handle checks.
  Null standard handles are required when the host itself owns a console, as
  on the GitHub runner, or PowerShell can bypass the pseudoconsole for its
  prompt.
- `cmd/windows-contract-probe` exposes only bounded test modes for runner facts
  and real Qt-to-Go IPC. It is not a production daemon.

### Reusable Qt Windows work

- `AppSettings` uses Windows LocalAppData, while `DaemonClient` preserves the
  named-pipe server name rather than applying Unix pathname rules.
- `DaemonLauncher` resolves only a sibling `aisummoner-client.exe`; its Windows
  detached launch redirects standard handles to the null device and requests
  `CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP` with `SW_HIDE`.
- The GUI has an `asInvoker` manifest and performs the same ordinary-user
  privilege decision as the Go contract. The CMake executable uses the
  Windows GUI subsystem.
- Windows-aware Qt tests and a dedicated IPC executable validate the existing
  bounded newline-JSON status/events path against the real Go listener.

### CI and packaging

- `.github/workflows/windows-remote-contract.yml` is serial, uses at most two
  build workers, has a 35-minute timeout and pins every action by commit SHA.
- The native matrix is intentionally one `windows-2022/amd64` job: Go 1.23.0,
  Qt 6.8.3 `win64_msvc2022_64`, Visual Studio 2022 x64 and `windeployqt`.
- PowerShell native-command failures are promoted to step failures. This gate
  was added after an earlier run demonstrated that `$ErrorActionPreference`
  alone could allow a failed `go test` to continue.
- `deploy/windows-spike/README.txt` is bundled and explicitly prevents the
  probes from being mistaken for or renamed into a production Core.

## Final Windows Evidence

- Source commit under test:
  `06f018ebf8e186da01ed2e72e2789b3657c4a56c`.
- Successful workflow:
  [Windows Remote contract run 33330465430](https://github.com/totrytakeoff/AISummoner/actions/runs/33330465430),
  job `99308003850`, 2m34s.
- Runner: Windows Server 2022 Datacenter, OS build `10.0.20348`, runner
  `2.336.0`, image `windows-2022` version `20260824.284.2`.
- Go: `go1.23.0 windows/amd64`; all seven top-level/subtest contract groups
  passed in 9.547s. The ConPTY test strictly observed `101x37`, Chinese UTF-8,
  Ctrl-C recovery, three clean sessions and bounded handle growth.
- Qt: Qt 6.8.3, MSVC `19.44.35228.0`, Windows SDK `10.0.26100.0` targeting
  `10.0.20348`; Release build passed and CTest was 1/1 in 2.52s.
- Real Qt-to-Go IPC passed after the Go probe created a unique `LOCAL\...`
  pipe and the MSVC Qt client completed status/events requests.
- Artifact:
  [AISummoner-Windows-Remote-Contract-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33330465430/artifacts/9737515477),
  ID `9737515477`, retained through `2026-09-13T19:20:07Z`.
- GitHub artifact wrapper: 58,103,279 bytes, SHA-256
  `6b202c29913d95e249c6e809681c0d0431c328aadfde50cd43ae544ffe3c39a0`.
- Inner engineering ZIP: 58,132,663 bytes, SHA-256
  `9c0e203995efb96872afb160fe30b933f62034916a2c1f91c00dcc822daeab10`;
  the downloaded hash matches the checksum file created on the Windows runner.
- The bundle contains 57 entries and the expected Qt Core/Gui/Network/Svg/
  Widgets DLLs, Windows platform/style/TLS plugins, the official 25,635,768-byte
  `vc_redist.x64.exe`, GUI, two probes, facts and warning README. The future
  installer must execute the redistributable when the target lacks that runtime.

The hosted runner token was Session 2, high integrity (`0x3000`) and elevated.
That usefully proves the pure privilege policy rejects this context, but the
packaged GUI itself cannot be live-launched as a valid normal-user app on this
runner. It is not ordinary-desktop evidence.

## Local And Cross-Build Evidence

- Local Linux Qt build and CTest: PASS, 1/1 in 2.34s, using serial CTest.
- Windows-only Go contract package/CLI cross-compilation and `go vet`: PASS in
  a Go 1.23.12 container with two CPUs, `GOMAXPROCS=2` and `-p=1`.
- Workflow syntax: PASS with actionlint 1.7.7.
- The production `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build
  ./cmd/aisummoner-client` correctly remains red. Its first boundary is
  `internal/sshserver/server.go`: Windows has no Unix `Setpgid`, `Setsid`,
  `Setctty`, `unix.PidfdOpen`, `PidfdSendSignal`, `Waitid` or related constants.
  This is the intended input to Task024's common-platform extraction, not a
  defect hidden by the probe binary.
- A local full Linux Go run reached one existing timing-sensitive DSH failure:
  `TestStartHostTimeoutCleansExactChild` used a 40ms context before its fixture
  wrote `child.pid`. It repeated under the constrained container; no DSH code
  was changed in Task023 and this is not represented as a Windows regression.
- No local Windows VM was available (`virsh` had no guests), so no disk-heavy
  VM image was created. ASD and all of its running services were untouched.

## Honest Failure And Retry Record

1. Run `33328397200` selected Ninja/MinGW against an MSVC Qt package and failed
   at link. The workflow now fixes the Visual Studio 17 2022 x64 generator.
2. Run `33328626376` built Qt but Qt-to-Go IPC timed out. The first pipe-peer
   implementation used `ImpersonateNamedPipeClient` before Qt wrote a request;
   exact client-PID/token verification removed that protocol-order race.
3. Run `33328892816` appeared green and produced an artifact, but log review
   found failed cwd and ConPTY assertions that PowerShell had not propagated as
   a step failure. That artifact is rejected; native fail-fast was added.
4. Run `33329166562` then honestly failed: Windows reported the cwd once as an
   8.3 short path, and the ConPTY attribute had been passed the address rather
   than the handle value. Cwd now compares directory identity and the attribute
   follows Microsoft's bare-HPCON contract.
5. Run `33329414854` proved cwd fixed but showed the PowerShell prompt escaping
   to the runner console. Explicit null standard handles isolated the child.
6. Run `33329569637` was fully green, but its resize check only logged an
   initial-attach-dependent `80x24`. It was not accepted as final resize proof.
7. Run `33329804575` strictly observed `101x37`, then exposed a test race: a
   resize repaint was counted as the post-Ctrl-C prompt. The final test records
   the prompt count immediately before interrupt and waits for a new prompt.
8. Run `33329946125` passed every strict runtime contract, but artifact
   inspection found no compiler runtime because `VCINSTALLDIR` had not been
   initialized. Its package is rejected even though the workflow was green.
9. Run `33330292751` loaded `vcvars64` and showed `windeployqt` correctly
   collecting `vc_redist.x64.exe`, while an over-specific gate expected three
   loose runtime DLLs and stopped the upload. Qt's deployment contract requires
   the official Redistributable for MSVC release builds, so the gate now checks
   that signed package instead of encouraging unsupported DLL copying.
10. Run `33330465430` passed every strict contract and produced the accepted
    artifact above. The setup-go cache restore warning is non-fatal and only
    caused a fresh dependency cache; all gated commands ran successfully.

## Remaining Task023 Acceptance Gates

1. Run the same contracts from a non-elevated interactive account on a clean
   Windows 11 x86-64 VM, then smoke Windows 10 22H2. Record exact OS builds.
2. Use a second local logon to prove the named-pipe DACL/peer check and DPAPI
   ciphertext both reject the wrong user. Current CI has only one runner logon.
3. Replace the probe with the production Core only after Task024/025 extraction,
   then verify GUI auto-start, no console flash, GUI-close daemon survival,
   single instance, pause/resume and crash recovery.
4. Prove the real WSS/yamux/strict-SSH PowerShell exec and Browser Terminal
   paths in Tasks026/027, including disconnect cleanup and no descendants.
5. Run `windeployqt` output on a clean machine without Go/Qt installed. The
   current artifact has a collected dependency closure but no clean-VM launch
   claim, production Core, installer or Authenticode signature.

ADR-0007 therefore remains `Proposed`. No independent approval is inferred;
the user retained the existing solo-implementation direction for this work.
