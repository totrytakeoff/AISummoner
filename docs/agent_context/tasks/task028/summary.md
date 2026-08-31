---
task_id: task028
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 028 Summary: Windows Ordinary-User Qt Lifecycle And Engineering Package

## Outcome

The Windows Remote Client now has a production-shaped Qt GUI package that
launches the trusted sibling Go Core as a hidden detached console process,
uses authenticated same-logon Named Pipe IPC, and leaves the Core alive when
the GUI closes. The hosted proof ran under a fresh non-elevated standard local
account on Windows Server 2022; it is not a Windows 10/11 VM or support claim.

The package is portable, unsigned engineering output. It contains the GUI,
Core, Qt runtime/plugins, VC runtime closure, license texts, notices, build
manifest and Task028 README, with no probes or private data. Git Bash/MSYS2,
WSL and pwsh are not bundled or required; Windows-native PowerShell/ConPTY
remains the execution backend and future shell profiles may be optional.

## Important fix

`LOCALAPPDATA` and `USERPROFILE` inherited from the GitHub runner caused the
first ordinary-user run to resolve data under `C:\Users\runneradmin` and the
Core exited with `Access is denied`. Qt and Go now resolve Profile and
LocalAppData from the current process token, with a regression test proving
poisoned inherited environment variables cannot redirect Device Identity.

## Verification

- Workflow: [Windows Remote contract run 33374475203](https://github.com/totrytakeoff/AISummoner/actions/runs/33374475203)
- Commit: `4f1ab26102eb42bc929531fdb48d99a40f776c04`
- Windows job `99432738248`: all Go contracts, Qt Release/CTest, Qt↔Go pipe
  interop, package assembly and ordinary-user GUI/Core lifecycle passed.
- Linux job `99432738089`: production and Qt regression gates passed.
- Inner ZIP SHA-256: `3320bbb394b0a926d9b83d67e41857ee9ac4a32610b66cf471fa994a35e872e5`
- Uploaded artifact: [AISummoner-Windows-Remote-Engineering-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33374475203/artifacts/9751443604)
  (artifact ID `9751443604`; uploaded archive digest
  `54415bc461bcfd12b4488899cb8e84d06051f027d9ef795b3749add9a1e7c59d`)

## Review boundaries

This is ready for human review, not an independent review verdict. The
artifact remains unsigned, ADR-0007 remains Proposed, and Windows 10/11 clean
VM validation, installer/Authenticode and public release remain open. Task029
owns target-aware Windows execution profiles and the real DSH Agent turn.
