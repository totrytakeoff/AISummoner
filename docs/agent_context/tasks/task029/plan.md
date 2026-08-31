---
task_id: task029
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 029 Plan: Target-Aware Windows Agent Execution Profile

## Objective

Enable the first real DSH Agent Turn against a Windows Remote Client while
keeping the Server as the sole Device/session authority and preserving the
existing Linux DSH behavior. The first slice is target-aware metadata and a
native `windows-powershell` Execution Profile; OpenCode, Codex and Claude Code
remain unchanged.

## Scope

- Extend the provider-neutral Agent run context with the selected Device's
  platform, architecture and a target-aware default working-directory policy.
- Resolve that metadata from the owner-scoped Store Device immediately before a
  Turn; an Adapter must never accept a Device ID or platform from model text.
- Add DSH profile selection at session creation. Linux keeps `/` and the
  existing `aisummoner` preset; Windows uses a pinned profile declaring
  PowerShell 5.1 syntax, Windows path rules and `windows-powershell` execution.
- Keep `remote_exec` JSON provider-neutral. Omitted `cwd` is the supported
  Windows default (the Remote Core resolves the current user's Profile); an
  explicitly supplied cwd must remain an absolute Windows path.
- Make DSH tool/rendered errors distinguish invalid Windows cwd, PowerShell
  execution failure, timeout and approval denial without leaking command text,
  keys or local paths.
- Add focused unit tests for metadata propagation, Linux compatibility, profile
  selection, Windows default cwd and fail-closed unsupported targets.
- Add one bounded hosted Windows E2E: login → paired Windows Device → DSH
  Session → one benign PowerShell `remote_exec` Turn → streamed final answer;
  keep the existing Linux DSH E2E green.

## Shell policy

Windows-native PowerShell 5.1/ConPTY is the required backend. Git Bash/MSYS2,
WSL and `pwsh` are optional future Execution Profiles only: never bundle them,
never select them implicitly, and never use them to translate commands for the
native profile. A later explicit user-selected `git-bash` profile may discover
an installed executable under a separately reviewed trust/quoting contract.

## Security and compatibility invariants

- The Device selected by the authenticated Server session remains the only
  execution target; DSH cannot choose a Device or rewrite target metadata.
- Existing approval, timeout, output limits, SSH host-key verification, TLS,
  pairing and Remote ACL/Job/ConPTY boundaries remain unchanged.
- No Server-local shell/filesystem/web/subagent capability is enabled in DSH.
- Unsupported platform/profile combinations fail closed before a provider Turn
  is sent. Windows Agent availability is not advertised until the full E2E
  passes.
- Do not claim Windows 10/11 support from hosted Server 2022 evidence; keep
  clean VM, installer, Authenticode and public release gates separate.

## Verification and resource gate

Use bounded focused Go tests, static inspection and the existing hosted Windows
workflow. Do not run local race/Qt/AppImage builds while the host remains below
the 4 GiB free-swap gate. ASD is not touched. Record exact run/job/artifact
hashes in the task summary and leave ADR-0007 Proposed pending human review.

## Out of scope

OpenCode/Codex/Claude adapters, structured file/patch tools, desktop streaming,
installer/signing, automatic shell discovery, bundled Git Bash/WSL, and public
Windows support claims.
