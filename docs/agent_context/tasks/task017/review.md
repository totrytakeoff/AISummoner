---
task_id: task017
type: review
status: approved
from: reviewer
to: orchestrator
revision: 2
decision: APPROVED
next_action: next_task
---

# Task 017 Review

## Decision

APPROVED

## Findings

No blocking issues found.

Non-blocking observations:

- The 1259 px single-panel boundary is intentionally based on the worst legal
  Session width, so a smaller current rail preference may use single-panel mode
  even where its own three columns could fit. This is a conservative,
  deterministic Task017 tradeoff: it preserves operability and guarantees that
  no valid persisted width can reopen an invisible Terminal.
- The A→B→A regression catches the revision 1 single-token failure and the
  implementation deletes only the completing Device's Set entry. A future
  test could make that second property even more mutation-sensitive by
  completing A while B remains pending, revisiting B, and asserting B still
  has exactly one request; the current keyed implementation is nevertheless
  correct and unambiguous.

## Revision 2 Closure Verification

1. **Maximum-width layout and Terminal visibility — closed.**
   `WORKSPACE_SINGLE_PANEL_MAX` is derived as
   `SESSION_SIDEBAR_MAX + AGENT_CENTER_MIN + DOCK_MIN - 1`, or 1259
   (`web/src/workspace/layout.ts:2-9`). `WorkspaceFrame` uses that exported
   value for accessibility state (`WorkspaceFrame.tsx:117-144`),
   `WorkspacePage` uses it for all three focus-return paths
   (`WorkspacePage.tsx:181-203,242-250`), and CSS exposes the navigation plus
   selected full-width zone through the same 1259 px boundary
   (`web/src/styles.css:372-386`). With a 400 px rail, the focused frame tests
   assert visible, reachable Tools at both 1140 and 1259 px, then all three
   non-inert zones and the actual 300 px dock separator at 1260 px
   (`WorkspaceFrame.test.tsx:130-177`). The pure solver yields exact
   `400 / 560 / 300` columns at 1260. The recorded real browser boundary smoke
   additionally verifies the CSS presentation that jsdom does not calculate.
2. **Per-Device Session creation admission — closed.**
   In-flight admission is a `Set<string>` keyed by Device; create adds before
   its first await and `finally` deletes only `requestedDeviceID`
   (`WorkspacePage.tsx:88,190-214`). Returning to a Device derives its visible
   `creating` state from that same Set (`WorkspacePage.tsx:129-140`), so an A
   request cannot be overwritten by B and an A completion cannot clear B. The
   empty-Device A→B→A regression holds both requests pending, proves exactly
   one create for each Device before completion, and projects only A after the
   route returns (`WorkspacePage.test.tsx:183-239`). Parent ownership also
   remains singular: embedded `AgentPage` is rendered only after a selected
   Session exists.
3. **Narrow Session-action focus — closed.**
   A real Session row selection switches to Agent and queues focus on the
   stable Agent navigation button; a user-triggered New/Retry passes the focus
   intent and does the same only after successful creation
   (`WorkspacePage.tsx:181-203,295-328`). Automatic first-Session creation does
   not steal focus. The 1024 px regression enters Sessions, activates a real
   row, repeats with the real New control, and after each transition asserts
   that the visible Agent navigation button owns focus
   (`WorkspacePage.test.tsx:303-337`). Closing Tools retains the same behavior.

## Regression And Architecture Verification

- Revision 0 accessibility fixes remain intact: hidden/maximized zones are
  `aria-hidden` and inert, the Agent zone is a labelled `section` rather than a
  nested `main`, icon controls remain named, reduced motion is respected, and
  separator values come from rendered conceded widths.
- Create and refresh failures retain the last valid Session index with working
  retry/dismiss actions. Session selection still closes the prior EventSource
  and unmounts Terminal; Device route change unmounts Terminal, removes the
  Agent surface, and ignores late snapshot/create projection.
- Direct unauthenticated and valid cross-owner `view=index` tests remain in the
  unchanged API hash. The Store still combines user, Device, current-owner and
  non-revoked predicates in one bounded query, while the response excludes
  user/provider-external IDs and full prompt history.
- Queryless `GET .../agent-sessions` retains the legacy full-snapshot contract;
  only the exact single `view=index` query selects the summary index, and
  invalid/extra query parameters remain rejected.
- The revision 2 changes stay within Task017 Controller behavior and tests. No
  DSH/runtime/event-v2, provider, Remote Qt, public transport, desktop/file,
  arbitrary docking, dependency, or data-plane expansion was introduced.

## Reviewer Verification

- Command: `git diff --check`
  Result: PASS, no whitespace errors.
- Command: read-only Node import/static probe of `layout.ts` plus the Workspace
  CSS media boundary.
  Result: PASS — derived constant, exported constant and CSS boundary are all
  1259; maximum-rail solver results are dock-auto-hidden at 1140/1259 and exact
  `{sessions:400, agent:560, dock:300, dockAutoHidden:false}` at 1260.
- Command: `sha256sum` over every Server and Workspace source named in revision
  2 `summary.md`.
  Result: PASS; every abbreviated hash matches the current full hash. Revision
  2 core values include `WorkspacePage.tsx` `57e29fa0...cc55`, its test
  `a78bb829...c25c`, `WorkspaceFrame.test.tsx` `aa1293da...f4b4`, `layout.ts`
  `a0c0c0d2...81e7`, and `styles.css` `e45d7633...cc73`; unchanged Server and
  Workspace hashes also match.
- Command: Codgent document validation for Task017 plan, summary and pre-review
  history.
  Result: PASS for all three inputs.
- Review: mandatory project/baseline/ADR context, Task017 plan, revision 2
  summary, complete revision 0/1 history, latest full diff/source/tests,
  ownership/privacy, legacy compatibility, stale results, Terminal/EventSource
  disposal, responsive/accessibility behavior, and scope.
  Result: all Task017 acceptance criteria and both prior review rounds are
  satisfied.
- Heavy Web, Go, race, ASD, build and browser suites were not repeated under
  the review instruction. Their revision 2 recorded results, exact source
  hashes, failure/retry record and cleanup evidence were inspected; no claim is
  elevated beyond the recorded evidence.

## Next Action

- Accept Task017 and advance to the next narrowly planned Alpha task.

---

# Revision 1 Review History (Preserved)

```yaml
task_id: task017
type: review
status: changes_requested
from: reviewer
to: orchestrator
revision: 1
decision: CHANGES_REQUESTED
next_action: revise_same_task
```

## Decision

CHANGES_REQUESTED

## Findings

Findings are ordered by severity.

1. **High — an allowed persisted Session width reopens the invisible live-Terminal defect from revision 0.** The rail is explicitly resizable and persisted through 400 px (`web/src/workspace/layout.ts:2-4`, `web/src/pages/WorkspacePage.tsx:73-75,160-164`). At a 1140 px frame with that valid preference, `solveWorkspaceColumns(1140, 400, 360, false, true, false)` returns `{sessions:400, agent:740, dock:0, dockAutoHidden:true}` because three usable zones need `400 + 560 + 300 = 1260` px (`layout.ts:36-51`). React nevertheless enters single-panel mode only through 1139 px (`WorkspaceFrame.tsx:125-139`), and the CSS that exposes the selected full-width Tools view has the same fixed 1139 px ceiling (`styles.css:372-386`). Opening Terminal still mounts it and selects Tools (`WorkspacePage.tsx:227-233`), but at 1140–1259 px the dock remains zero-width, `aria-hidden`, and inert with no visible mobile navigation. Thus the Terminal WebSocket can again be live but unreachable. The 1024 px regression uses the default 280 px rail and does not cover the supported persisted width (`WorkspacePage.test.tsx:244-264`, `WorkspaceFrame.test.tsx:130-153`).
2. **Medium — first-Session creation is not actually deduplicated per Device during the required rapid Device switching path.** `creationInFlight` stores only one Device ID (`WorkspacePage.tsx:87,186-205`), while every route change resets the automatic-attempt marker (`WorkspacePage.tsx:128-140,209-214`). If an empty Device A has a pending create, switching to empty B overwrites the ref with B; switching back to A then starts a second A request. If the first A request resolves while A2 owns the same string token, its `finally` can also clear A2's guard and re-enable creation. The revision 1 test proves one click cannot duplicate a single stationary Device's request (`WorkspacePage.test.tsx:151-180`), but it does not exercise the A→B→A case required by `AGENTS.md` for Workspace changes.
3. **Medium — focus recovery remains incomplete when a narrow-screen Session action changes the visible panel.** Selecting a Session synchronously switches from Sessions to Agent (`WorkspacePage.tsx:180-184`); successful creation does the same (`WorkspacePage.tsx:186-198`). `WorkspaceFrame` then makes the Sessions zone hidden and inert (`WorkspaceFrame.tsx:125-144`), leaving focus on a row/New button that just disappeared, or allowing the browser to fall back to the document. The only explicit focus transfer is in `closeDock` (`WorkspacePage.tsx:235-244`). Revision 1 therefore fixes focus after closing Tools, but not the Session/view changes called out in revision 0 and the Alpha focus-recovery requirement. No test selects or creates a Session from the narrow Sessions panel and asserts a visible focus target.
4. **Test/evidence gap — the new regressions close most revision 0 gaps but miss the remaining boundary and race cases above.** There is now direct unauthenticated and cross-owner index coverage, real focusable inert children, a 1024 px Tools test, error preservation/retry, and Terminal unmount assertions for both Session and Device changes. Approval still requires focused coverage using legal maximum rail width around the actual fit boundary, rapid empty-Device A→B→A creation, and focus after a narrow Session selection/create.

## Required Fixes

- Make the visible single-panel state follow the layout solver's actual inability to render a usable dock for every legal width preference, not only the fixed 1139 px default-width boundary. React accessibility state, CSS presentation, navigation visibility, and focus restoration must share that condition. Add a regression proving an opened Terminal is visible and reachable with a 400 px rail at 1140 and 1259 px, with the expected three-zone transition at 1260 px.
- Track creation ownership with a per-Device/request-specific in-flight identity (or cancel and join superseded requests) so A→B→A cannot issue concurrent creates for A and an older completion cannot clear a newer guard. Add the rapid empty-Device route regression.
- When a narrow Session selection or successful creation hides the focused Sessions zone, move focus to a stable visible Agent control/heading. Add an assertion against the real focusable Session control.

## Verified Revision 0 Fixes

- The fixed 1139 px fallback makes the default-width 901–1139 concession band show the selected Tools panel, and hidden/maximized zones now receive both `aria-hidden` and `inert`.
- The Agent zone is now a labelled `section`, avoiding a nested `main`, and resize separators report the rendered conceded widths.
- WorkspacePage is now the only empty-index creation owner, so the former parent/embedded-Agent competing paths are gone; the remaining finding is the cross-Device in-flight implementation.
- Create and refresh errors retain the last valid Session rows and expose retry/dismiss actions.
- Session changes unmount Terminal and close the prior EventSource; Device route changes unmount Terminal and ignore the late create result. Focused tests now exercise both Terminal disposal paths.
- `view=index` now has direct unauthenticated coverage and a valid foreign-owned Device/Session request that returns the indistinguishable empty result (`internal/agentapi/api_test.go:290-374`).

## Verified Architecture, Security, And Scope

- The bounded index remains one Store query with user, Device, current-owner, and non-revoked predicates. Its public projection excludes user ID and provider external Session ID, bounds/normalizes the first-user-message title, and preserves unknown/unowned indistinguishability.
- The legacy queryless Device Session endpoint still returns the newest full snapshot; only the exact single `view=index` query selects the bounded index, and invalid/extra query parameters are rejected.
- Explicit Session snapshots remain Device-validated, stale index/snapshot/create results are guarded before projection, and Agent EventSources close on Session unmount/change.
- No DSH runtime/source, Agent event-v2, new provider adapter, desktop/data-plane capability, public transport, Remote Qt change, or new dependency entered Task017.

## Reviewer Verification

- Command: `git diff --check`
  Result: PASS, no whitespace errors.
- Command: `node --experimental-strip-types --input-type=module -e "...solveWorkspaceColumns..."`
  Result: PASS as a deterministic static probe; with Session width 400 and dock preference 560, widths 1140, 1200 and 1259 all return `dock:0,dockAutoHidden:true`, while 1260 returns a usable 300 px dock. This confirms finding 1 without opening a transport.
- Command: `sha256sum` over every Server and Workspace file named in `summary.md`.
  Result: PASS; all full hashes match the revision 1 abbreviated hashes, including `WorkspacePage.tsx` `ab330a0f...39dd`, `WorkspaceFrame.tsx` `7f0efda7...e047`, `layout.ts` `4ef79a6d...05ae`, `styles.css` `90e39520...c2f6`, and `internal/agentapi/api_test.go` `787b43ec...185f`.
- Review: complete current Task017 diff/source/tests, mandatory baselines/ADRs, revision 0 review, revision 1 summary, ownership/privacy boundary, legacy API, stale-result/transport lifecycle, responsive/accessibility behavior, and scope.
  Result: the Server ownership/privacy and legacy compatibility work remains sound, and most revision 0 fixes are confirmed; the three Controller defects above prevent approval.
- Heavy Web, Go, race, ASD, build, and browser runs were not repeated under the review instruction. The coder's recorded 59-test/build/ASD results and hashes were inspected, but those runs do not cover the adversarial cases above.

## Next Action

- Revise Task017 in place, add the three focused regressions, rerun the relevant Web tests/build and recorded gates, and return revision 2 for independent review.

---

# Revision 0 Review History (Preserved)

```yaml
task_id: task017
type: review
status: changes_requested
from: reviewer
to: orchestrator
revision: 0
decision: CHANGES_REQUESTED
next_action: revise_same_task
```

## Decision

CHANGES_REQUESTED

## Findings

Findings are ordered by severity.

1. **High — the Terminal/Device dock becomes invisible while remaining live at common laptop widths.** `solveWorkspaceColumns` needs at least `280 + 560 + 300 = 1140` px for the default three zones and otherwise returns a zero-width dock (`web/src/workspace/layout.ts:35-50`). The explicit Sessions/Agent/Tools fallback is enabled only at 900 px or below in both React and CSS (`WorkspaceFrame.tsx:124-127`, `styles.css:361-375`). At, for example, 1024 px, clicking Terminal still sets `terminalMounted=true` (`WorkspacePage.tsx:209-215`), so a Terminal WebSocket is opened inside a zero-width, `aria-hidden` column with no visible dock controls. This breaks the legacy Terminal migration and the required narrow-width fallback, and creates an invisible live control session.
2. **Medium — visually hidden workspace zones remain keyboard-focusable, and the landmark structure is invalid.** `WorkspaceFrame` only puts `aria-hidden` on zero-width/non-selected columns (`WorkspaceFrame.tsx:140-142`); it does not make their buttons, search input, composer, tabs, or terminal inert. This is especially visible when the dock is maximized or auto-hidden: keyboard focus can enter controls the user cannot see. Closing/switching a panel also has no focus restoration. In addition, `WorkspaceFrame` renders a `<main>` inside `AppShell`'s existing `<main>` (`AppShell.tsx:26`, `WorkspaceFrame.tsx:141`). The Alpha accessibility contract requires keyboard-operable panel changes, valid semantic structure, and focus recovery.
3. **Medium — a fresh online Device can create two default Sessions concurrently.** An empty index renders `AgentPage` with no selected ID; that surface automatically posts a `per_command` Session after legacy latest returns 404 (`AgentPage.tsx:97-138`). During that request, the rail's New conversation control remains enabled because it only knows the parent `creating` flag (`SessionRail.tsx:75-80`), and the parent owns a second independent create path (`WorkspacePage.tsx:178-196`). A user click can therefore issue two create requests and leave an unintended empty Session. One component must own empty-device initialization, or the shared creation state must disable/deduplicate the other path.
4. **Medium — a create error permanently replaces otherwise usable Session rows.** `WorkspacePage` passes `sessionsError || createError` as the rail-wide error (`WorkspacePage.tsx:258-268`), while `SessionRail` renders errors instead of its groups (`SessionRail.tsx:99-106`). `createError` is not cleared by selecting a Session or by a successful index refresh. One failed New conversation attempt therefore hides all existing rows until the user retries creation or leaves the Device. Mutation errors should be shown without destroying the last valid index and should have a deterministic clear/retry path.
5. **Test gap — several plan-mandated regressions are not represented by the 52-test suite.** The Workspace tests cover 1440 px and 390 px behavior but not the 901–1139 px concession band; they do not cover an empty online Device, index/create error behavior, Terminal disposal after Session change, or Terminal disposal after Device route change. The narrow accessibility test uses non-focusable placeholder children, so it cannot detect focus into hidden real controls. The new API test checks an unknown Device but does not directly exercise unauthenticated `view=index` or a valid cross-owner request. These cases should accompany the fixes rather than relying on the existing broad handler/store tests.

## Required Fixes

- Make dock concession and explicit single-panel fallback agree across the entire width range where three usable zones do not fit. Opening Terminal must always produce a visible surface, or explicitly switch/maximize to one; do not open an unreachable hidden Terminal transport.
- Remove hidden zones from keyboard interaction (`inert`, actual hidden/conditional presentation, or equivalent), restore focus after close/view changes, use one valid main landmark, and report separator values from the actual rendered width when concession shrinks a panel.
- Establish one deduplicated Session-creation owner for the empty-index path and preserve the default `per_command` behavior.
- Keep the last valid Session list usable when create/refresh mutations fail, with a clear and retryable error presentation.
- Add focused regressions for the medium-width dock, focusability/landmarks, empty-device creation, error recovery, and Terminal disposal on both Session and Device changes. Add direct index authentication/owner-hiding coverage.

## Verified Areas

- The Store index is a single fixed-limit query with user, Device, current-owner, and non-revoked predicates; it orders by update/create/ID and keeps same-owner re-pair from reviving revoked Sessions.
- The public summary projection excludes `user_id` and `external_session_id`, bounds and normalizes the first user-message title, and returns an empty safe surface for unknown/unowned Devices.
- `GET .../agent-sessions` without a query retains the legacy full-snapshot path; only the explicit single `view=index` query selects the index and caller-controlled limits are rejected.
- Explicit Session snapshots validate their returned Device before display. Request sequence guards ignore late Device/index/create results, and `useAgentEvents` closes the prior EventSource when its active Session ID changes.
- No DSH runtime/source, event-v2 schema, provider adapter, desktop capability, public transport, or Remote Qt change entered Task017.

## Reviewer Verification

- Command: `git diff --check`
  Result: PASS, no whitespace errors.
- Command: `sha256sum` over the plan, summary, Server index/API files, Workspace core files, and stylesheet.
  Result: PASS; every abbreviated implementation hash recorded in `summary.md` matches the current file.
- Review: full Task017 diff plus relevant Store/API/Agent/Terminal hooks, tests, Alpha baseline, and ADR-0003/0004.
  Result: ownership/privacy, legacy compatibility, and scope boundaries are sound; the Controller findings above prevent approval.
- Heavy test/ASD execution was not repeated under the reviewer's read-only/no-heavy-test instruction. The coder's recorded results were inspected but are not treated as a substitute for the missing behavioral cases.

## Next Action

- Revise Task017 in place, rerun the focused Web regressions plus the recorded full gates, and return revision 1 for independent review.
