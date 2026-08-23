---
type: todo
status: active
updated_by: planner
---

# Todo

## Current

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

## Next

- [ ] Independent combined Task015/Task016 review; no approval is inferred from
  the bounded external reviewer timeout.
- [ ] Non-root Ubuntu real pairing/status/session/pause/resume/GUI-close E2E.
- [ ] Resume Controller Workspace as Alpha A3 only after Remote GUI approval.

## Required Task015 Boundaries

- [x] No Server/public protocol changes and no false Terminal/Agent stream
  classification.
- [x] Pairing code only through verified local status response, never event/log.
- [x] Pause and shutdown join Tunnel streams before publishing completion.
- [x] Legacy CLI remains usable while systemd gains daemon mode.

## Historical Residuals

- Task014 remains human-accepted but its independent review record is not
  fabricated.
- Current ASD deployment and CLI AppImage remain rollback/test assets, not the
  final Qt distribution.
- The earlier detailed Controller task draft is superseded, not implemented;
  its product requirements remain in Baseline 04.

## Blocked

- No architecture blocker. Go verification must run on the controlled ASD
  environment because this local host currently has no Go toolchain.
