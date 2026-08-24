---
task_id: task021
type: plan
status: in_progress
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 021 Plan: DSH Session Parity And Recovery

## Status

Implementation in progress. The human tester has completed the first real DSH
chain and reported five concrete Controller defects in permission handling,
credential recovery, replay ordering, Session lifecycle and Settings access.

## Owner

Primary implementation agent, working without delegated coding agents at the
user's request.

## Reviewer

Independent reviewer after the implementation and bounded verification freeze.

## Context

Task020 proved Browser -> AISummoner -> private DSH -> approved Remote command ->
SSH -> Device execution. The current Controller deliberately retained a narrow
MVP projection. Human testing now shows that projection does not yet preserve
the interaction contracts users rely on in DSH: Session permission state is not
directly configurable, missing credentials are reported as a generic failed
Turn, replay can place a command card after final output, and Session lifecycle
actions plus global Settings are incomplete.

AISummoner remains the only user, Device, Session, approval, audit and Remote
execution authority. DSH remains a private provider runtime. This task mirrors
DSH's interaction lifetimes without importing DSH's local-shell permission
authority or exposing its Host to the Browser.

## Goal

Make the existing DSH-first Controller usable as a resumable daily Agent UI:
permission labels must equal enforced policy, missing credentials must be
actionable without discarding the Session, replay must preserve command-before-
final ordering, command cards must support one-click collapse, and Sessions
must be archivable/restorable/deletable from both the rail and global Settings.

## Relevant Files

- `migrations/`
- `internal/store/agent.go`
- `internal/agent/service.go`
- `internal/agent/types.go`
- `internal/agentapi/`
- `internal/dsh/adapter.go`
- `internal/app/dispatcher.go`
- `cmd/aisummoner-server/server.go`
- `web/src/api/`
- `web/src/agent/`
- `web/src/workspace/SessionRail.tsx`
- `web/src/components/ControllerSettingsDialog.tsx`
- `web/src/pages/AgentPage.tsx`
- `web/src/pages/WorkspacePage.tsx`
- `web/src/pages/DevicesPage.tsx`
- `web/src/styles.css`

## Required Behavior

### Permission truth and DSH-aligned lifetimes

- The current Session exposes exactly two honest policies: `执行命令前询问`
  (`per_command`) and risk-gated `完全访问` (`full_access`).
- Changing the current Session updates the authoritative Store only while it is
  idle or failed. A concurrently starting Turn either observes the committed
  new mode or makes the policy update fail; it must never display one policy and
  enforce another.
- Settings persists a default for future Sessions only. It never silently
  rewrites existing Sessions. Full access requires an explicit warning and
  confirmation in both lifetimes.
- DSH never receives independent authority to approve Remote commands.

### Credential readiness and recovery

- The private DSH Adapter exposes only value-free credential status from
  `credentials.describe`: configured and writable, never the key or source.
- A DSH Turn is rejected before persisting a new user message when the required
  key is missing, using a stable `PROVIDER_CREDENTIAL_REQUIRED` API error and a
  direct Settings action.
- Existing failed Sessions remain selectable and can run again immediately
  after a key is configured. Configuration refreshes the visible status without
  creating a replacement Session.
- Provider details, credential values and raw DSH errors remain absent from
  logs, audit, SQLite and Browser responses.

### Ordered replay and tool-card control

- The persisted final assistant projection sorts after every tool card from
  that Turn when a Session is reopened. User input remains before the tools.
- Live reasoning, tool and final-output separation remains unchanged.
- The conversation exposes one bounded `折叠全部命令` action which closes all
  expanded command cards without deleting output or changing tool state.

### Session lifecycle

- Session rows expose Archive and Delete actions without nested interactive
  controls. Archive is non-destructive and immediately removes the Session from
  the active rail; Delete requires confirmation and removes the product
  Session plus its message/tool projection through foreign-key cascade.
- Archive, restore and delete are owner-scoped, fail closed for running/waiting
  Sessions, close stale SSE subscribers and are safe against lookup/admission
  races.
- Settings contains a bounded owner-scoped archived Session list, grouped with
  Device identity, and supports restore and confirmed delete.
- Active indexes and automatic latest-Session recovery never select archived
  Sessions. Direct cross-owner access remains indistinguishable from missing.

### Settings entry points

- The Device Hub exposes the same global Settings dialog as a Workspace.
- Global Settings omits Device-only destructive controls when no Device is in
  scope, while General, Agent/default-permission and Session Management remain
  available.

## Required Changes

- Add a forward-only migration for `archived_at` plus per-user Agent defaults.
- Extend Store/Service/API with bounded active/archived projections, current
  permission update, future default update, archive/restore and delete.
- Add DSH credential describe/preflight and exact safe HTTP status endpoints.
- Make final transcript persistence use completion ordering for replay.
- Add controlled tool-card collapse state and DSH-style permission selector.
- Refactor Session rail actions and Settings reuse on the Device Hub.
- Update dispatcher path-shape tests and all affected API/type projections.

## Verification

All heavy commands are serial and fail-stop. No Go, Node or Docker commands run
in parallel.

```bash
GOMAXPROCS=2 go test -count=1 -p 1 ./internal/store ./internal/dsh ./internal/agent ./internal/agentapi ./internal/app ./cmd/aisummoner-server
GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/agent ./internal/agentapi
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run --maxWorkers=1
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Focused deterministic regressions must prove:

- current/default permission separation, full-access confirmation and an atomic
  policy-update/Turn-start race;
- missing-key preflight creates no message/Turn, configured recovery reuses the
  same product/external Session, and status responses are value-free;
- reopened user -> command -> final ordering plus collapse-all behavior;
- archive/restore/delete ownership, active-Turn rejection, cascade, bounded
  archived index and stale SSE closure;
- Device Hub Settings, optional Device section and rail action focus/selection.

## Documentation Requirements

- Record exact implementation hashes, low-memory commands, resource gates and
  every real failure/retry in `summary.md`.
- Keep the DSH permission-model deviation explicit: AISummoner has no claimed
  workspace sandbox; its two policies govern only the reviewed Remote command
  capability.
- Update user-facing documentation if an endpoint or operating behavior changes.

## Out Of Scope

- DSH local filesystem/shell/search tools or a Browser-direct DSH UI.
- OpenCode, Codex or Claude Code rich adapters.
- Session rename, fork, workspace browsing or provider-native transcript purge.
- Remote Qt Client changes, desktop streaming and filesystem permission UX.
- Parallel heavyweight validation or unrelated ASD service changes.

## Acceptance Criteria

The task is ready for review when all five human-reported defects are covered by
deterministic tests, the low-memory focused/race/Web gates pass, the deployed
DSH Session can change policy and recover after credential configuration without
replacement, archived Sessions can be managed from global Settings, and no
unrelated runtime or deployment service is disturbed.
