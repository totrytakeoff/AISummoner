---
task_id: task017
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 017 Plan: Controller Workspace Foundation

## Goal

Replace the Controller's disconnected Device, Terminal and Agent pages with the
Alpha device-scoped Control Workspace: a bounded recent Session rail on the
left, the existing ordered Agent conversation in the center, and an optional
Terminal or Device Activity dock on the right. Preserve the proven execution
and approval boundaries while making the normal path Login → Device Hub →
Workspace.

## Inputs

- `docs/baseline/04-alpha-product-direction.md`
- `docs/decisions/ADR-0003-agent-adapter-ui.md`
- `docs/decisions/ADR-0004-alpha-clients-and-agent-runtime.md`
- Task014 ordered Agent UI/provider behavior and Task016 Remote GUI handoff
- `/home/myself/workspace/deepseek-harness` layout, sidebar and conversation
  packages as an interaction reference only; no DSH runtime or source import

## Architecture Boundary

- The Server remains authoritative for User, Device and Agent Session
  ownership. The Browser may keep only non-sensitive presentation preferences.
- Add one fixed-limit, owner-and-current-device-checked recent Session index.
  Do not expose provider external session IDs, user IDs, credentials, prompt
  bodies beyond a bounded first-user-message title, or revoked Sessions.
- Keep the existing Agent snapshot/SSE/approval and Terminal WebSocket
  protocols unchanged. Switching Device or Session must close the old SSE and
  prevent late results from changing the new selection.
- Implement a fixed three-zone CSS Grid with small pointer/keyboard resize
  primitives. Do not add a docking framework before the fixed layout is proven.
- DSH event v2, Capability Descriptors, Runtime Session lifecycle, Markdown,
  queue/steer/retry/cancel and new provider adapters belong to Alpha A4+.

## Required Server Behavior

- Extend `GET /api/v1/devices/{device_id}/agent-sessions` with the explicit
  `view=index` query. The existing request without that query continues to
  return the latest full snapshot for migration compatibility.
- Return at most 50 Sessions ordered by `updated_at`, `created_at`, then ID,
  descending. Each item contains only product Session fields plus a bounded,
  single-line title derived from its first user message or a fixed untitled
  fallback.
- The Store query must combine user, non-revoked Session, Device ID and current
  Device owner predicates in one statement. Missing/unowned Devices expose an
  empty/not-found-safe surface without leaking another user's Session count.
- Reject unknown `view` values; do not accept caller-controlled unbounded
  limits.

## Required Controller Behavior

### Device Hub and routing

- Device cards open `/devices/{id}/workspace`; the workspace is the primary
  control route. Legacy Device, Agent and Terminal URLs remain safe migration
  surfaces and lead into the same Workspace rather than dead links.
- The workspace fills the viewport below the authenticated application bar and
  retains Device name, Online/Offline status, Device Hub navigation and a
  settings/detail entry.

### Session rail

- Expanded default width 280 px, keyboard/pointer resizable within 240–400 px,
  and collapsible to a 56 px control rail. Persist only the width/collapsed
  preference in `localStorage`.
- Provide New conversation, local search, Today/Earlier grouping, provider and
  live state indicators, deterministic empty/loading/error states, and a
  selected row tied to the center conversation.
- New conversations always start in `per_command`; `full_access` remains an
  approval-time escalation for that Session, never an entry mode prompt.

### Agent center

- Extract/reuse the existing Agent page as an embeddable surface. It must load
  an explicitly selected Session snapshot, close the previous EventSource on
  selection, retain reasoning/tool/output separation and keep approval in the
  composer position.
- A new Session or provider reconfiguration updates the rail and selection.
  A stale response from a previous Device/Session must be ignored.

### Optional right dock

- Default closed. Tabs are Terminal and Device Activity; opening Terminal
  mounts the existing strict Terminal panel against the selected Device.
- Expanded default width 360 px, keyboard/pointer resizable within 300–560 px,
  collapsible, and maximizable. Session changes close the dock; Device route
  changes dispose Terminal and Agent transports.
- At narrow widths provide explicit Sessions / Agent / Tools views instead of
  squeezing three unusable columns. No desktop streaming surface is added.

## Visual Direction

- Follow DSH's calm persistent shell and VS Code/Zed's workspace hierarchy,
  but retain AISummoner identity and security language.
- Use a neutral low-chrome surface, one centered conversation axis, a sticky
  rounded composer, compact session rows and subtle separators. Avoid a
  dashboard-card appearance inside the active workspace.
- All icon-only controls require accessible names. Resize separators expose
  `role=separator`, current values and arrow-key operation. Respect reduced
  motion.

## Required Tests

- Store: ordering, fixed cap, title normalization, current-owner isolation,
  revoked exclusion and unpair/re-pair non-revival.
- Agent API/service: legacy latest snapshot unchanged; `view=index` response,
  unknown view, authentication and cross-owner isolation.
- Web API: typed Session index parsing path and no secret persistence.
- Workspace: route migration, Device→Workspace navigation, empty/error/search/
  grouping, Session switching, new Session selection, exact one active
  EventSource, old-session late result rejection, dock open/close/maximize and
  Terminal disposal on Device change.
- Layout: pure column concession/clamp behavior plus pointer and keyboard
  resizing, collapsed rail and narrow-view fallback.
- Preserve the existing Agent, Terminal, login and Device test suites.

## Verification

Run serially behind the repository resource gate:

```bash
npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
GOMAXPROCS=2 go test -count=1 -p 1 ./internal/store ./internal/agent ./internal/agentapi ./internal/app
GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/agent ./internal/agentapi
GOMAXPROCS=2 go test -count=1 -p 2 ./...
GOMAXPROCS=2 go vet ./...
GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

## Out Of Scope

- Standard Agent event v2, DSH adapter/runtime, OpenCode/Codex/Claude adapter
  work, Runtime capability configuration or Browser-stored provider secrets.
- Rename/archive/delete/fork Session mutations, pagination beyond the bounded
  recent index, arbitrary IDE docking, files/editor, desktop streaming/input.
- Remote Qt Client changes, public TLS/deployment changes or data-plane changes.

## Acceptance

Task017 is review-ready when a signed-in user selects a Device and stays in one
device-scoped Workspace where recent conversations can be created and switched,
the current Agent and Terminal work concurrently, panels resize/collapse with
mouse and keyboard, narrow screens remain operable, and changing Device or
Session cannot leak transport events or authority across the new selection.
