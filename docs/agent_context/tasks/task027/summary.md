---
task_id: task027
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 027 Summary: Windows ConPTY Terminal And Joined Lifecycle

## Outcome

The production Windows Remote Core now serves interactive SSH PTY sessions
through the inbox Windows PowerShell 5.1 and ConPTY. The same native Device
challenge, TLS/WSS, yamux, strict Device SSH host-key verification and embedded
SSHD chain has passed VT/UTF-8 I/O, Windows cwd, `101x37` resize, Ctrl-C and
continued interaction on Windows Server 2022. Normal root exit, channel close,
context cancellation and Tunnel shutdown also prove joined Job/ConPTY/pipe/
worker cleanup while an unrelated process survives.

This checkpoint does not claim a supported Windows desktop release. The hosted
runner used an elevated account, so ordinary-user Windows 11/10 GUI launch,
no-console-flash, GUI-close daemon survival and clean-VM acceptance remain
Task028. Server-owned Windows cwd/Execution Profile and a real DSH PowerShell
Turn remain Task029. The artifact is unsigned and ADR-0007 stays Proposed.

## Files Changed

- `internal/winprocess/conpty_windows.go` adds the shared production ConPTY
  launcher with fixed system PowerShell, UTF-8 setup, bounded cwd/dimensions,
  synchronous pipes, explicit null standard handles, suspended Job assignment
  and caller-owned native resources.
- `internal/windowsprobe/process_windows.go` reuses the production ConPTY
  primitive while retaining its non-PTY explicit handle-list probe.
- `internal/sshserver/process_windows.go` replaces the interactive-shell
  rejection with concurrent ConPTY input/output pumps, resize, Ctrl-C,
  TERM/KILL, natural-exit draining and idempotent joined cleanup.
- `internal/winprocess/process_windows_test.go` covers invalid ConPTY launch
  dimensions without allocating resources.
- `internal/sshserver/process_windows_test.go` repeatedly launches, resizes and
  exits the real shell while checking native handle cleanup.
- `internal/sshclient/windows_integration_test.go` proves the production PTY
  through the full native network/SSH chain and covers normal exit, channel
  close, context cancel and Tunnel shutdown descendant cleanup.
- `deploy/windows-spike/README.txt` now identifies native ConPTY Terminal as
  working and keeps Agent/release support explicitly unavailable.

## Behavior Changed

### Native Terminal

- `pty-req` plus `shell` now launches system Windows PowerShell 5.1 through
  ConPTY. It does not search `PATH`, select `pwsh`, require Git Bash/MSYS2 or
  fall back to `cmd.exe`/WSL.
- Terminal input and the single combined VT/UTF-8 output stream are copied
  concurrently. The requested Windows cwd is applied before launch; omitted
  cwd retains the existing Profile Known Folder policy.
- `window-change` calls `ResizePseudoConsole` under the same close-safe state
  lock. Browser/xterm Ctrl-C is transported as byte `0x03`; PTY `signal INT`
  maps to the same write and reports its actual result. TERM/KILL terminate the
  complete Job with the existing 143/137 contract.
- Each shell is created suspended and assigned to a kill-on-close Job before
  resume. The pseudoconsole attribute is the only inherited process attribute,
  and null standard handles prevent a console-attached launcher/CI parent from
  bypassing ConPTY.

### Joined lifecycle

- Natural PowerShell exit terminates remaining descendants, closes input and
  the pseudoconsole in a resize-safe order, drains the terminal tail and joins
  all copy workers before completion.
- Channel/input close, context cancellation and Tunnel shutdown terminate the
  whole Job, unblock I/O and release every owned handle. Resize, signal and
  cleanup calls are synchronized and idempotent.
- Closing one Terminal does not terminate an unrelated process or the Device
  Tunnel; the context-cancel case proves a second session can still be opened.

### Shell strategy

Windows remains native-first. Git Bash/MSYS2 is not bundled or required. A
future installed-Git integration may only be an explicitly selected
`git-bash` Execution Profile; native PowerShell/ConPTY remains the required
fallback and the true target OS is never hidden from the Agent.

## Verification

### Final native Windows and Linux CI

- Source under test: `bc329649c8f1abe4951389af2cd88009e304b6a1`
  (`7062ae6` implementation, `6a92abd` full-chain assertion correction and
  `bc32964` retained probe handle-list import).
- Workflow: [Windows Remote contract run 33363125225](https://github.com/totrytakeoff/AISummoner/actions/runs/33363125225),
  conclusion `success`.
- Windows job `99398274719`: completed in 3m46s.
  - `internal/winprocess`: PASS in 0.204s.
  - Production `internal/sshserver`: PASS in 5.840s; repeated real ConPTY
    launch/resize/exit/native-handle cleanup passed in 2.01s and the preserved
    non-PTY cleanup path passed in 3.81s.
  - Task026 PowerShell exec full chain: PASS in 1.51s.
  - Task027 ConPTY full chain: PASS in 2.87s, including normal-root-exit,
    channel-close, context-cancel and Tunnel-shutdown descendant cleanup
    subtests (0.54s, 0.54s, 0.54s and 0.53s).
  - Task023 probe after shared ConPTY reuse: PASS in 4.828s; its direct ConPTY
    probe passed in 1.54s.
  - Windows vet, production Core build, Qt 6.8.3/MSVC Release CTest (1/1), Qt
    named-pipe `status.get`/`events.list`, deployment and artifact packaging:
    PASS.
- Linux job `99398275016`: Ubuntu 22.04 normal/race/vet regressions passed in
  41s.
- Low-memory-safe local checks used capped Docker formatting/static tooling plus
  `git diff --check`. Native Go/race/Qt work was intentionally delegated to
  bounded CI because local free swap was below the documented 4 GiB gate. ASD
  was untouched.

Both final jobs emitted non-fatal GitHub action Node-runtime and setup-go cache
warnings. Every required test/build/package command still ran and passed.

### Failure and retry evidence

- [Run 33362729647](https://github.com/totrytakeoff/AISummoner/actions/runs/33362729647)
  failed Windows job `99397130243` after the production ConPTY unit tests had
  passed. The full-chain test retained Task026's stale expectation that PTY
  creation should fail and sampled the prompt count before resize redraw,
  mistaking that redraw for the Ctrl-C prompt. Linux job `99397130367` passed.
- Commit `6a92abd` removed only the obsolete rejection assertion and sampled
  the stable prompt count after the resize marker. [Run 33362924150](https://github.com/totrytakeoff/AISummoner/actions/runs/33362924150)
  then passed the complete production ConPTY chain and all lifecycle subtests,
  but Windows job `99397686531` failed later because the Task023 probe still
  needed `unsafe` for its distinct non-PTY `HANDLE_LIST` code. Linux job
  `99397686730` passed.
- Commit `bc32964` restored that required import without changing runtime
  behavior. Final run `33363125225` passed every Windows/Linux/package gate.

### Engineering artifact

- Artifact: [AISummoner-Windows-Remote-Engineering-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33363125225/artifacts/9747422615),
  ID `9747422615`, retained through `2026-09-14T06:14:00Z`.
- GitHub artifact wrapper: 67,554,446 bytes, digest
  `sha256:9f4ac542dc0671153a013a736938146c6a292a72b281877f5cca330cfbdb9f16`.
- Inner engineering ZIP: 67,590,616 bytes, SHA-256
  `4d728492f866617a55576b455fed4690677dbe6ae4c18d6571d5fb33be68494f`;
  `sha256sum -c` passed against the runner-created checksum.
- Local ignored copy:
  `dist/task027-windows-ci-bc32964/AISummoner-Windows-Remote-Engineering-x86_64.zip`.
- The ZIP contains 58 files / 117,324,236 expanded bytes, including the real
  Core, Qt GUI, IPC/contract probes, Qt runtime and Visual C++ Redistributable.
- Runner facts report `SessionID=2`, integrity RID `12288` (`0x3000`) and
  `Elevated=true`. This is engineering evidence, not ordinary-user desktop or
  release acceptance.

## Deviations From Plan

- No functional scope deviation. The requested Browser/xterm semantics were
  exercised at the SSH wire contract and production backend boundary; a
  physical Browser on a Windows 11 desktop remains part of the Task028/public
  E2E evidence rather than this elevated CI checkpoint.
- No ASD deployment, public release, installer, signing or external server
  mutation was performed.

## Known Issues / Follow-Up

- Task028 must prove ordinary-user GUI launch of the trusted sibling Core with
  no console flash, GUI-close daemon survival, pause/recovery and a clean
  Windows 11/10 engineering package.
- Task029 must add Linux-Server target-aware Windows cwd, the trusted
  `windows-powershell` Agent Execution Profile, a real DSH Turn, installer,
  signing and final public Server/Controller E2E.
- Different-logon named-pipe/DPAPI rejection still needs a second local Windows
  account.
- Tasks025-027 remain independent-review debt. This coder handoff provides no
  review verdict and does not accept ADR-0007.
