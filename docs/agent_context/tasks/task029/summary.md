---
task_id: task029
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 029 Summary: Target-Aware Windows Agent Execution Profile

## Outcome

The Server and DSH Agent path now understand the selected Remote as a trusted,
immutable execution target. An owned `windows/amd64` Device selects the native
`windows-powershell` profile, Windows path rules and current-user Profile cwd
policy; Linux keeps its existing POSIX/user-shell behavior. Unsupported or
incomplete target combinations fail before a provider Turn is sent.

The provider-neutral `remote_exec` contract remains the sole command boundary.
For Windows, omitting `cwd` reaches the Remote and resolves to the current
user's Profile Known Folder. An explicit cwd must be a clean absolute drive or
UNC path and is checked first for target syntax, then against the real Remote
filesystem. Invalid cwd, PowerShell start/failure, timeout and approval denial
remain distinguishable without returning command text, credentials or host
paths in stable errors.

No Git Bash, MSYS2, WSL or `pwsh` dependency was bundled, discovered or selected
implicitly. Windows PowerShell 5.1 is the only native profile in this slice.
OpenCode, Codex and Claude Code adapters were not changed.

## Implemented Boundaries

- `internal/agent` derives `ExecutionTarget` only from the authenticated
  owner-scoped Device immediately before Session/model/Turn operations. The
  same frozen target is passed to optional Runtime session capabilities and the
  Remote executor; model text cannot supply a Device, platform or shell.
- `internal/dsh` selects one of two reviewed embedded presets. The Windows
  persona declares native PowerShell and Windows paths, while the shared HMAC
  Capability Bridge still exposes only the bounded Remote command tool. The
  Adapter rejects incomplete or unsupported profiles before starting DSH.
- `internal/sshclient` validates path syntax according to the target rather
  than the Linux Server host. Windows drive/UNC paths are accepted; relative,
  slash-rooted, device-namespace, dirty or invalid-character paths fail closed.
  Remote `env` rejection becomes `REMOTE_CWD_INVALID`, while process-start
  failure becomes `REMOTE_POWERSHELL_FAILURE`.
- `internal/sshserver` validates an explicit cwd while answering the SSH `env`
  request and revalidates at `exec` to close the TOCTOU window. An omitted cwd
  stays omitted until the Windows backend resolves the user Profile.
- `web` renders and replays the new stable failures as localized tool errors,
  not as command output.
- `.github/workflows/windows-remote-contract.yml` now separates native Windows,
  Linux regression and real pinned DSH jobs. The DSH job checks out exact
  upstream commit `47f943d279dc2f90d8623ec44d8ec1f524966b36`, verifies the pinned
  Node/pnpm inputs, packages the actual Host and runs the Windows preset Turn.

## Real Runtime And Native Evidence

Final source commit: `9446c91d61703999b013566593bc8066d1b5afa9`.

- Workflow: [Windows Remote contract run 33636189891](https://github.com/totrytakeoff/AISummoner/actions/runs/33636189891)
- Windows job `100267539014`: all native Go security/Core/PowerShell/ConPTY and
  Tunnel/SSH tests, Qt Release/CTest, Qt-to-Go named-pipe interop, package
  assembly and ordinary-user staged GUI/Core lifecycle passed. Omitted Agent
  cwd resolved to the actual user Profile. The handle probe converged across
  equal execution windows at `188 -> 194 -> 194`, distinguishing bounded Go/
  Windows worker initialization from a continuing per-exec leak.
- Linux job `100267538874`: Agent/App/DSH/SSH production regressions and Linux
  Qt tests passed.
- Pinned DSH job `100267538671`: built and verified Node `24.19.0` plus DSH
  `0.1.0-rc.5`, loaded both reviewed presets, then ran the real DSH Host against
  a deterministic OpenAI-compatible provider. The model's DSH `bash` call was
  HMAC-bridged to `remote_exec` with the exact PowerShell command and omitted
  cwd; the provider received the tool result and DSH streamed the final
  `WINDOWS_DSH_CHAIN_OK` answer.
- Artifact: [AISummoner-Windows-Remote-Engineering-x86_64](https://github.com/totrytakeoff/AISummoner/actions/runs/33636189891/artifacts/9849067518),
  ID `9849067518`, uploaded archive digest
  `13732328442a2bfa4bb944e40d2a42fdc8afe85a806ea5dbe43969a656f0549b`.
  The downloaded inner ZIP passed `unzip -t`; independently recomputed SHA-256
  is `d2c5a76256a54779f4cafc31ef73c5b3f5116c3b6beb846e54a8463e939e36a5`.
  Its ordinary-user proof records `elevated=false`, integrity RID `8192`,
  authenticated same-logon IPC, sanitized child environment and Core survival
  after GUI exit.

Local bounded gates also passed: focused Go packages and target/cwd tests in the
pinned serial container, Windows `amd64` test-binary cross-compilation, all 87
Web tests across 16 files, the production Web build, workflow/script syntax,
and both real pinned-host tests against the locally packaged DSH Runtime.

## Failed Attempts And Fix

Runs
[33634487769](https://github.com/totrytakeoff/AISummoner/actions/runs/33634487769)
and
[33635293442](https://github.com/totrytakeoff/AISummoner/actions/runs/33635293442)
passed Linux, pinned DSH and every native Windows data-plane E2E but stopped on
the PowerShell handle-count assertion (`188 -> 194`). The identical fixed
increase after larger warm-up and probe initialization indicated bounded
process-wide initialization rather than a per-command slope. The final test
keeps a bounded first-window cap and a stricter convergence cap over a second
equal window; the successful `188 -> 194 -> 194` trace now
detects a continuing leak without treating bounded runtime worker handles as
one.

## Evidence Boundary And Remaining Gates

The two hosted runtime halves meet at the typed `RemoteExecInvoker` contract:
one proves the actual pinned DSH Host/provider/tool/follow-up stream, and one
proves the actual Windows Device/TLS/WSS/yamux/strict-SSH/PowerShell path. This
is stronger than fixture-only or cross-compile evidence, but it is not a single
cross-machine Browser login -> pairing -> DSH -> Windows run. That literal
deployment proof remains a release gate because isolated GitHub jobs provide
no shared private cross-OS network or persistent Server.

ASD was not accessed or modified. The artifact remains an unsigned engineering
ZIP. Clean Windows 11 and Windows 10 22H2 desktop runs, second-logon rejection,
installer/upgrade/uninstall, Authenticode and the final public deployment E2E
remain open. ADR-0007 therefore stays Proposed, and this handoff requests human
review rather than asserting approval.
