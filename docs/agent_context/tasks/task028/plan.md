---
task_id: task028
type: plan
status: ready_for_review
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 028 Plan: Windows Ordinary-User Qt Lifecycle And Engineering Package

## Status

Implementation and bounded native CI evidence are complete and ready for human
review under the active user goal to complete the native
Windows Remote Client. Task027 is ready for human review; Tasks025-027 retain
independent-review debt. Advancing this bounded desktop/package slice neither
fabricates those verdicts nor accepts Proposed ADR-0007.

## Owner

Primary implementation agent, working without delegated coding agents as the
user requested.

## Reviewer

Human reviewer. A passing hosted-runner job is evidence, not an independent
review verdict or Windows 11/10 support declaration.

## Context

Tasks025-027 provide a production Windows Core with ordinary-interactive-token
policy, LocalAppData, DPAPI/ACL Identity, authenticated named-pipe IPC,
PowerShell/Job exec and ConPTY Terminal through the real native Tunnel/SSH
chain. The reusable Qt port already has Windows paths/endpoint handling, a GUI
subsystem target, an `asInvoker` manifest, sibling-Core argument validation and
`QProcess::startDetached` configuration. Its tests currently prove only the
launch specification and that `MainWindow::closeEvent` sends no pause request;
they do not launch a detached child, observe no-console state or prove survival
after the launcher exits.

Qt 6.8 documents that the instance `startDetached()` honors program,
arguments, working directory, standard-file redirection and the Windows
CreateProcess argument modifier. Task028 keeps that small implementation but
adds native behavioral evidence instead of replacing it with an unneeded shell
or bundled Git Bash layer.

## Goal

Deliver a checksummed unsigned Windows engineering package whose GUI can be
started by a normal desktop user, launches only the trusted sibling production
Core without a console window, reconnects over the authenticated local pipe and
leaves the Core/Tunnel alive when the GUI closes. The package must carry its
license, notices, runtime prerequisites and explicit engineering/support
status, and must be testable without Go/Qt build tools in the target profile.

## Relevant Files

- `desktop/remote-client/src/daemonlauncher.*`
- `desktop/remote-client/src/platformsecurity.*`
- `desktop/remote-client/src/main.cpp`
- `desktop/remote-client/tests/test_remoteclientui.cpp`
- new Windows-only desktop lifecycle/package probe sources under
  `desktop/remote-client/tests/`
- `desktop/remote-client/CMakeLists.txt` and Windows resources
- `cmd/aisummoner-client/` Windows manifest/build inputs if required
- `.github/workflows/windows-remote-contract.yml`
- `deploy/windows-spike/README.txt` or its Task028 package successor
- Windows design, ADR-0007 and durable agent context

## Required Behavior

### Trusted sibling launcher

- Resolve only `aisummoner-client.exe` beside the canonical GUI binary. Reject
  a missing/non-file/non-executable sibling, a different canonical directory
  and Windows reparse-point application directories or Core files; never
  search `PATH` or the working directory.
- Pass `daemon`, the canonical HTTPS origin, fixed LocalAppData directory and
  bounded optional device name as distinct arguments. Do not add development,
  root/elevation bypass, shell or arbitrary user-supplied arguments.
- Launch detached through Qt's supported Windows CreateProcess modifier with
  null standard streams, `CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP` and
  `SW_HIDE`. Launch failure must recover UI busy state and show a bounded local
  error; a PID alone is not treated as daemon readiness.
- Use the stable user Profile/Home as cwd so the Core never retains a package,
  temporary extraction or AppImage-style directory as its cwd.

### Ordinary-user and GUI lifecycle

- GUI and Core remain `asInvoker`; elevated/high-integrity, service-account or
  Session-0 execution fails closed with a clear ordinary-user message. Being a
  member of Administrators under a filtered token is not itself rejection.
- A native Windows lifecycle probe must use the production `DaemonLauncher`
  path to launch a console-subsystem child, observe no console attachment and
  exact arguments/cwd, exit the launcher process and prove the child remains
  alive until explicitly cleaned up.
- Add the strongest bounded hosted-runner ordinary-user proof available: use a
  fresh standard local account/profile or a separately recorded restricted
  medium token, run from the assembled stage with build-tool paths removed, and
  record the exact token/profile limitations. Never describe a synthetic or
  Server-2022 account as a clean Windows 11/10 VM.
- Exercise the production Core and authenticated same-logon named pipe in that
  ordinary context when the runner permits it. Closing the actual GUI must send
  no pause/shutdown and the Core must remain queryable; all test processes and
  temporary accounts/data must be cleaned up even on failure.

### Package contract

- Build Go windows/amd64 and Qt 6.8.3/MSVC Release natively, with the GUI using
  Windows subsystem and the Core retaining console-subsystem CLI semantics but
  an explicit `asInvoker` execution manifest.
- Run `windeployqt` into a fresh stage. Include only the production GUI/Core,
  required Qt/plugins/runtime prerequisites, `LICENSE`,
  `THIRD_PARTY_NOTICES.md`, a Task028 README and non-secret build manifest;
  contract probes/runner facts remain CI evidence rather than user-facing
  executables.
- Verify the stage has the Windows platform/style/TLS plugin closure, no
  build-host absolute paths, no PDB/private key/token/config data and no missing
  expected production/license files. Record unsigned status explicitly.
- Start/test from the staged directory with a sanitized environment before
  creating the ZIP. Produce a runner-side SHA-256 and upload the exact ZIP plus
  checksum. A future installer owns redistributable installation; the portable
  README must state that prerequisite accurately.

### Existing product invariants

- GUI still communicates only over authenticated local IPC, cannot read Device
  private keys, opens no network listener and does not become a second Device/
  Session authority.
- GUI close and manual Pause remain different: close keeps Core/Tunnel alive;
  Pause uses existing joined Core behavior.
- Linux GUI, daemon, IPC, Terminal/Agent and AppImage behavior remain unchanged.
- Git Bash/MSYS2/WSL is neither bundled nor used by launcher/package tests.

## Verification

- Windows Qt unit tests for reparse/sibling/arguments/cwd, launch failure/busy
  recovery, privilege policy and close-without-pause behavior.
- Native detached-lifecycle executable proves no console, launcher-parent exit,
  child survival and deterministic cleanup through the production launcher.
- Native standard-account or medium-token package smoke records token facts,
  staged GUI/Core execution, same-logon IPC and GUI-close Core survival where
  supported by the hosted runner.
- Inspect PE subsystem/manifests and unsigned status for GUI/Core; validate
  package inventory, licenses, Qt closure, VC redistributable and SHA-256.
- Preserve Task026 exec, Task027 ConPTY, Windows security/IPC/Core build/vet and
  focused Linux normal/race/vet. Run Linux Qt/AppImage gates when shared GUI or
  packaging behavior changes.
- Local work remains formatting/static inspection only because free swap is
  below the 4 GiB resource gate. Native compile/runtime evidence runs in the
  bounded GitHub workflow. ASD is not used.

## Documentation Requirements

- Record exact source/run/job/artifact hashes, token facts, package inventory,
  every failed attempt and whether the test was a clean profile versus clean
  VM.
- Update Windows design and ADR-0007 evidence without changing Proposed unless
  a human explicitly accepts it after the remaining Windows 11/10 gates.
- Keep Task029 target-aware cwd/Execution Profile/real DSH Turn, installer,
  Authenticode and final public Server/Controller E2E explicit.

## Out Of Scope

- Server target-aware Windows cwd, Runtime Execution Profile or Windows Agent
  enablement (Task029).
- Per-user installer, auto-start registration, updater, Authenticode signing or
  public stable release (Task029/final release gate).
- Windows 11/10 VM claims not actually exercised by available infrastructure.
- GUI visual redesign, Controller changes, desktop streaming or new local/
  remote control capabilities.
- Bundled/detected Git Bash, MSYS2, WSL or `pwsh`.

## Acceptance Criteria

The task is ready for review when:

- The real production launcher behavior—not only its spec—proves trusted
  sibling selection, no console, detached parent exit and child survival.
- The staged GUI/Core passes the strongest reproducible non-elevated profile
  smoke available in CI, with any hosted-runner gap named precisely rather than
  hidden.
- GUI close leaves Core online/queryable, while cleanup removes every test
  process/account/file and existing Pause/Tunnel joined semantics stay green.
- The native Windows and Linux regression jobs pass and a minimal, licensed,
  checksummed unsigned production-shaped ZIP is uploaded with exact hashes.
- Windows Agent stays unavailable, ADR-0007 stays Proposed and no independent
  review or Windows 11/10 support is fabricated.

## Resource Gate

Local heavy builds/tests require at least 8 GiB MemAvailable and 4 GiB free
swap with at most two CPUs. Current free swap remains below that gate, so
native and Linux compile/race/AppImage execution must use bounded CI; local
operations remain memory-capped. ASD is untouched.
