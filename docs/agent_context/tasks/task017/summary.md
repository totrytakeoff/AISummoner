---
task_id: task017
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 2
review_required: true
---

# Task 017 Summary: Controller Workspace Foundation

## Outcome

The first Controller refactor slice is implementation-complete at revision 2. A signed-in
user now enters a Device-scoped Control Workspace with a bounded recent Session
rail, the existing ordered Agent surface in the center, and an optional
Terminal or Device dock. Legacy Agent and Terminal URLs migrate into the same
Workspace. No Agent execution, approval, SSE or Terminal WebSocket protocol was
changed.

Revision 1 addressed every revision 0 finding class for the default-width path:
it extended the fallback, made hidden panels inert, removed the nested landmark,
centralized Session initialization, preserved the last usable index on errors,
and added the missing transport and ownership regressions. The revision 1
review then correctly exposed maximum-width, cross-Device and narrow-focus edge
cases within those same classes.

Revision 2 closes the three adversarial cases found in the revision 1 review:
the single-panel breakpoint is derived from the maximum legal rail plus minimum
Agent and dock widths; in-flight creation admission is held independently per
Device across A→B→A routing; and narrow Session selection or user-triggered
creation restores focus to the stable visible Agent navigation control.

## Server And Ownership Boundary

- `GET /api/v1/devices/{id}/agent-sessions?view=index` returns at most 50
  non-revoked Sessions ordered by update/create/ID descending. The legacy GET
  without a query still returns the newest complete snapshot.
- The Store evaluates user ID, Device ID, current Device owner and non-revoked
  state in one query. Unknown/unowned Devices return an empty index.
- The index projection does not select or return user IDs, provider external
  Session IDs, credentials or full prompt history. Its title is the normalized,
  80-rune maximum first user message with a fixed fallback.
- A real Unpair → same-owner re-pair regression proves old revoked Sessions do
  not reappear while a fresh post-pair Session does.

## Controller Behavior

- Device cards now enter `/devices/{id}/workspace`; old `/agent` and
  `/terminal` routes redirect safely, including `?dock=terminal`.
- The Session rail provides create, search, Today/Earlier grouping, provider and
  state labels, selected state, 280 px default sizing, 240–400 px pointer and
  keyboard resizing, and a 56 px collapsed mode. Only layout preferences enter
  `localStorage`.
- New conversations always request `per_command`. Explicit Session switching
  loads that owner-scoped snapshot, closes the prior EventSource, ignores late
  responses and closes any mounted Terminal dock.
- The right dock defaults closed, supports Terminal and Device metadata,
  300–560 px resizing, maximize/restore and explicit disposal on close, Session
  change or Device route change.
- At 1259 px and below, explicit Sessions / Agent / Tools views replace
  squeezed columns for every valid persisted rail width. Opening Terminal
  always switches to a full-width visible Tools surface; at 1260 px the maximum
  400 px rail, 560 px Agent floor and 300 px dock fit exactly.
- Hidden or maximized-away zones are both `aria-hidden` and `inert`, the Agent
  region no longer nests a second `main`, and closing Tools restores focus to
  the visible opener. Resize controls report the rendered, conceded width.
- An empty online Device has one parent-owned, per-Device deduplicated
  `per_command` creation path. A failed create or index refresh leaves existing
  Session rows usable and provides explicit retry/dismiss actions.
- The creation guard is a per-Device in-flight set, so rapid navigation cannot
  overwrite another Device's token or let an older completion clear a newer
  guard. Returning to an in-flight Device truthfully keeps its New control in
  the creating state.
- Desktop 1440×900 and mobile 390×844 browser smokes with mocked non-secret API
  data confirmed zero page overflow and the intended fixed-shell layout. A
  revision 1 browser smoke at 1024×768 additionally proved that opening
  Terminal produces a visible 1024 px Tools surface rather than a hidden live
  transport. Revision 2 repeated the boundary with a persisted 400 px rail:
  1259 px rendered full-width Tools, while 1260 px rendered exact
  `400 / 560 / 300` columns.

## Files Changed

- `internal/store/agent.go`, `agent_test.go` — safe bounded Session index and
  owner/re-pair tests.
- `internal/agent/service.go` — Session-list service boundary.
- `internal/agentapi/api.go`, `responses.go`, `api_test.go` — explicit index
  query, safe response and compatibility/security tests.
- `web/src/api/*` — typed Session-summary client.
- `web/src/App.tsx`, `App.test.tsx`, Device/AppShell pages — Workspace routing
  and migration surfaces.
- `web/src/pages/AgentPage.tsx` — reusable explicit-Session embedded surface.
- `web/src/pages/WorkspacePage.tsx` and tests — Device-scoped orchestration,
  stale-request defense and transport disposal.
- `web/src/workspace/*` — Session rail, three-zone frame, layout solver, dock
  and focused tests.
- `web/src/styles.css` — desktop/mobile low-chrome Workspace presentation.
- Task017 plan and shared roadmap/context/state/todo documents.

## Verification

- Final `npm --prefix web test -- --run` — PASS: 14 files, 62 tests.
- `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build` — PASS:
  TypeScript and Vite production build, 72 modules. Existing non-blocking
  584.74 kB chunk warning remains.
- Revision 1 Playwright browser smoke at 1024×768 and revision 2 smoke at the
  1259/1260 px maximum-rail boundary — PASS. The latter measured full-width
  Tools at 1259 and exact `400 / 560 / 300` columns at 1260. Temporary
  screenshots and Vite processes were removed after inspection.
- ASD fresh isolated focused Go test — PASS:
  `./internal/store ./internal/agent ./internal/agentapi ./internal/app` at
  `0.066s / 1.593s / 5.267s / 0.069s`.
- ASD focused race — PASS: `./internal/agent` `6.679s` and
  `./internal/agentapi` `11.660s` in revision 0; the revision 1 rerun passed at
  `6.705s / 12.285s`.
- ASD `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 600s ./...` — PASS: all
  28 reported targets. The revision 2 exact-tree rerun also passed, including
  Agent API `5.499s`, HTTP API `5.362s`, and Tunnel `1.767s`.
- Revision 2 ASD focused race — PASS: `./internal/agent` `6.617s` and
  `./internal/agentapi` `11.708s`.
- ASD `GOMAXPROCS=2 go vet ./...` — PASS, no output.
- ASD `GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server
  ./cmd/aisummoner-client` — PASS, no output.
- `git diff --check` — PASS.
- Revision 1 ASD began with `4,726,564 kB` available memory and no Go jobs.
  The exact staging `/tmp/aisummoner-task017-r1.AhtkBa` was deleted; final
  available memory was `4,721,336 kB`, with `1,987,084 kB` free swap and no
  residual Go jobs. Existing services/containers were not changed.
- Revision 2 used a fresh exact-tree staging with matching aggregate SHA-256
  `d447e4ad...bbc3` and no `gofmt` differences. It began with `4,722,784 kB`
  available memory; `/tmp/aisummoner-task017-r2.8TK12E` was deleted after all
  gates, with `4,711,196 kB` available, `1,986,828 kB` free swap and no Go
  jobs. Existing services/containers were not changed.

## Honest Failure And Retry Record

1. A new pointer test initially produced `NaN` because jsdom lacks native
   `PointerEvent` coordinates. The fixture now constructs an explicit DOM
   pointer event; the production resize implementation was unchanged.
2. The first Go focused run passed Store/Agent/App but the new API ordering test
   moved a Session to a fixed time older than its real creation time. Adding a
   controlled Service clock exposed a second existing latest-session fixture
   tie. The clock was made mutex-protected and monotonically increasing; the
   third identical four-package run passed.
3. The first browser mock intercepted Vite's `/src/api/client.ts` because its
   route glob was too broad; the corrected path-only fixture then exposed a
   real missing `flex-direction: column` in the embedded Agent surface.
4. The first mobile smoke exposed a real inherited 54 px flex-basis that
   overlapped the Agent toolbar and runtime bar. The narrow toolbar now sizes to
   content. Both desktop and mobile smokes were repeated successfully.
5. Independent revision 0 review correctly requested changes for the 901–1139
   px invisible dock, hidden-panel focusability, nested landmark, competing
   first-Session creators, destructive mutation-error rendering, and missing
   focused regressions. Revision 1 implements every required fix above.
6. The first revision 1 Web run passed 58/59 tests. Its only failure queried an
   `aria-hidden` region by accessible name, but hidden content intentionally has
   no accessible name. The test now inspects the element/label directly; the
   production implementation was unchanged and the final run passed 59/59.
7. The first revision 1 production build found one test-only TypeScript error:
   DOM `TimerHandler` is typed as `Function`, while the deterministic interval
   fixture stores `() => void`. A local type assertion was added; the final
   typecheck and Vite build passed.
8. Independent revision 1 review found that a valid 400 px persisted rail moved
   the worst-case fit boundary to 1260 px, that the single creation token was
   not per-Device under A→B→A routing, and that narrow Session actions did not
   restore focus. Revision 2 replaces the token with a per-Device set, derives
   the shared 1259 px breakpoint from legal maxima, and restores focus after
   both selection and user-triggered creation.
9. The first revision 2 focused run passed all eight frame cases but one
   A→B→A fixture used `findByText('other-host')`, which matched both the toolbar
   and Device Activity heading. The test now targets the toolbar identity; no
   production code changed, and the identical focused run passed 17/17 before
   the final 62-test suite.

## Deviations And Follow-Up

- No functional deviation from Task017. Device Activity intentionally contains
  current metadata and a truthful future-feed placeholder; no fake audit stream
  was introduced.
- Arbitrary docking, Session mutations/pagination, Agent event v2, DSH runtime
  adaptation, Markdown/queue/cancel and OpenCode/Codex/Claude Code adapters
  remain Alpha A4+ work.
- The current bundle-size warning should be addressed with route/vendor code
  splitting during the broader Controller UI refactor; it is not a correctness
  or security blocker for this foundation.

## Source Hashes

- Go index/API files: `agent.go` `2832d579...da71f`, `agent_test.go`
  `b66b3398...a25d`, `service.go` `b7661513...4454`, `api.go`
  `c372143a...ec30`, `api_test.go` `787b43ec...185f`, `responses.go`
  `31c0d6bf...7e7b`.
- Workspace core: `WorkspacePage.tsx` `57e29fa0...cc55`,
  `WorkspacePage.test.tsx` `a78bb829...c25c`, `WorkspaceFrame.tsx`
  `7f0efda7...e047`, `WorkspaceFrame.test.tsx` `aa1293da...f4b4`,
  `SessionRail.tsx` `cca1c4a3...a8b3`, `SessionRail.test.tsx`
  `1bb908c1...714`, `WorkspaceDock.tsx` `dd1a718c...d96`, `layout.ts`
  `a0c0c0d2...81e7`, `styles.css` `e45d7633...cc73`.
