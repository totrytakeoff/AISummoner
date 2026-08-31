---
type: todo
status: active
updated_by: coder
---

# Todo

## Current

- [x] task024 (ready for review): extracted build-tagged
  paths/privilege/lifecycle, Identity storage, local IPC transport and SSH
  process backends; Linux daemon/SSH/Qt/AppImage regressions pass and Tunnel
  hello is the strict `linux|windows` enum. Windows constructors remain absent
  for Tasks025-027, and Proposed ADR-0007 is not accepted by this checkpoint.

- [ ] task023 (in progress): Windows Server 2022 CI now proves the strict
  Qt↔Go named-pipe path, exact peer-token check, DPAPI/ACL, suspended Job,
  PowerShell/ConPTY runtime, Qt/MSVC build and unsigned contract bundle. The
  accepted CI evidence is run `33330465430` and includes the official Visual
  C++ Redistributable. Ordinary-user Windows 11/10, different-logon rejection,
  clean-VM/no-console-flash and production Core E2E remain before ADR-0007 can
  be accepted.

- [x] task022: freeze the common Runtime provider/model boundary, implement
  DSH-native multi-provider configuration and current-Session model selection,
  replace hard-coded DeepSeek credential preflight with selected-route
  readiness, and preserve interfaces for later configuration-file adapters.
  DSH is the only Runtime changed in this task. Implementation, low-memory
  Go/Web gates, authenticated model-selection smoke and bounded ASD deployment
  are complete; independent review and the human custom-provider/Turn smoke
  remain tracked release evidence.

- [x] task021: align current/default Session permissions with enforced Remote
  policy, add value-free DSH credential readiness and same-Session recovery,
  repair replay ordering/collapse-all, add archive/delete management, and expose
  global Settings from the Device Hub. Revision 1 also prevents five-second
  Device polling from resetting the selected Settings tab. Implementation,
  low-memory Go/Web gates and bounded ASD deployment are complete; the human
  tester accepted this as the first usable Controller milestone. Independent
  code review remains release debt.

- [x] task020: the human tester proved the real DSH Agent -> approved Remote
  command -> SSH -> Device chain. Follow-up usability defects are isolated in
  Task021 rather than weakening the accepted runtime/security boundary.

- [x] task019: replace the duplicate Device/Terminal toolbar actions with one
  component-dock launcher and localize the complete Controller UI into Chinese.

- [x] task018: DSH-first Controller rebaseline — retire old Manage, move Device
  and Agent configuration into Workspace Settings, port the pinned DSH visual
  and interaction system, and retain explicit Runtime adapter seams. Web
  66/66, production build and desktop/narrow visual smoke pass; awaiting the
  user's visual acceptance without a fabricated independent review.
- [x] The user accepted Task018's restored DSH visual baseline and authorized
  this two-finding cleanup before real DSH Runtime integration.

- [x] MVP-0 real Terminal/Agent chain accepted by the user.
- [x] Alpha direction and ADR-0004 created without weakening the data plane.
- [x] User selected Remote Client as the first refactor and Qt as the GUI
  toolkit; the previous Controller-first draft is superseded by the roadmap.
- [x] Qt 6.11 Core/Widgets/Network and CMake are available locally.
- [x] Claude Code completed a no-write UI review; Kimi CLI is not installed.
- [x] task015 Remote Core daemon/private IPC implemented, fully verified and
  frozen for the Qt consumer; final combined review remains due because the
  external delta re-review timed out without a verdict.
- [x] task016 revision 1 removes mandatory first-run Server entry, embeds a
  build-time default, automatically starts the daemon, keeps self-hosting under
  an advanced override, and has been rebuilt/reverified as `ready_for_review`.
- [x] Human accepted the zero-configuration Remote GUI direction and explicitly
  authorized starting the Controller refactor while the combined review remains
  tracked as release debt.
- [x] task017 scope frozen: bounded Session index plus Device Hub → fixed
  three-zone Control Workspace; Agent protocol/runtime v2 stays in Alpha A4.
- [x] task017 revision 1 implementation and verification frozen as
  `ready_for_review`: bounded owner-scoped Session index, unified Workspace,
  complete <=1139 px fallback, inert/focus semantics, single Session creator,
  non-destructive errors and the required transport/ownership regressions.
- [x] task017 revision 2: close the maximum-width 1140–1259 px dock boundary,
  per-Device A→B→A creation admission and narrow Session-action focus findings.

## Next

- [ ] task025: implement the real Windows per-user Runtime, LocalAppData/token
  gate, DPAPI Identity and authenticated named-pipe IPC against Task024's
  seams. A fail-closed unsupported SSH execution backend may keep pairing/
  status usable until Task026, but it must never report Terminal success.
- [ ] Complete Task023's ordinary-user Windows 11/10, wrong-logon and clean-VM
  evidence before accepting or revising Proposed ADR-0007. This validation now
  runs alongside the user-authorized, non-enabling Task024 extraction.
- [x] Publish the first Controller milestone to the public GitHub repository
  with a concise README, documentation index, quick-start/deployment guides,
  public roadmap, contribution guide and security policy.
- [ ] Independently review Task021; the deployed Controller milestone has
  already received human product acceptance.

- [ ] Independent combined Task015/Task016 review; no approval is inferred from
  the bounded external reviewer timeout.
- [ ] Non-root Ubuntu real pairing/status/session/pause/resume/GUI-close E2E.
- [x] Task017 Controller Workspace Foundation revision 2 independently reviewed
  and approved with the revision 0/1 history preserved.
- [x] Human accepted the Task019 UI correction and authorized the real DSH
  Runtime integration as the next slice.

## Required Task015 Boundaries

- [x] No Server/public protocol changes and no false Terminal/Agent stream
  classification.
- [x] Pairing code only through verified local status response, never event/log.
- [x] Pause and shutdown join Tunnel streams before publishing completion.
- [x] Legacy CLI remains usable while systemd gains daemon mode.

## Historical Residuals

- Task014 remains human-accepted but its independent review record is not
  fabricated.
- Current ASD deployment, CLI AppImage and Qt AppImage remain Alpha test/
  rollback assets; no stable GitHub Release is claimed yet.
- The earlier detailed Controller task draft is superseded, not implemented;
  its product requirements remain in Baseline 04.

## Blocked

- No active implementation blocker. Task023's remaining evidence needs a
  non-elevated Windows 11/10 desktop VM and, for the wrong-logon checks, a
  second local account. The user explicitly allowed those checks to follow the
  current GitHub Actions implementation pass.
- This host still has no native Go toolchain; bounded local Go builds use the
  pinned serial Docker environment. No Task023 verification requires or
  modifies ASD.
