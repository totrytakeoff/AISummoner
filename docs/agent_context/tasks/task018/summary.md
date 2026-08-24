---
task_id: task018
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 018 Summary: DSH-first Controller Rebaseline

## Outcome

The Controller now uses a source-aligned DSH light experience from Login and
Device Hub through the active Device Workspace. The approved Task017 transport
and ownership foundation remains intact, while the legacy Manage surface,
double application chrome, dashboard cards and custom Agent visual language
have been removed.

DSH is now an explicit first-class experience profile pinned to checkout
`47f943859bef60e4160492346772ded9b24f765a`. Actual Runtime labels remain
truthful: the current Session still identifies DeepSeek, OpenCode or Fake, and
future DSH/OpenCode/Codex/Claude Code Runtime work stays behind a separate
adapter boundary.

## Controller Behavior

- The authenticated Workspace occupies the full viewport. Its persistent
  desktop structure is Session rail → Agent conversation → optional
  Terminal/Device tools dock; <=1259 px retains Task017's explicit
  Sessions/Agent/Tools single-panel fallback.
- The Session rail, search, rows, reasoning disclosure, tool disclosure,
  assistant text, user bubble and floating composer use the pinned DSH geometry
  and light tokens. Runtime and approval state live quietly in the composer.
- The old Device Manage page and action are gone. `/devices/{id}` migrates to
  `/devices/{id}/workspace?settings=device`; legacy Agent and Terminal routes
  continue their safe Workspace redirects.
- A single DSH-style Settings modal owns General, Agent & Models and Device
  configuration. DeepSeek keys remain transient in Browser memory and are
  cleared when the modal closes; Device metadata and confirmed unpair live in
  the Device section.
- Login and Device Hub now share the same restrained presentation language, so
  entering the Workspace no longer switches visual systems.
- Controls use accessible inline SVG icons. Existing focus, inert, resize,
  transport disposal, reduced-motion and owner-scoped Session behavior is
  retained.
- Vite's local API proxy keeps `127.0.0.1:8080` as its default and accepts
  `AISUMMONER_WEB_API_ORIGIN` for isolated local previews without disturbing an
  occupied service.

## Architecture And Attribution

- ADR-0005 makes the pinned DSH Web UX the canonical Controller reference and
  distinguishes Experience/Presentation Adapter from Runtime Adapter.
- `web/src/agent/experience.ts` exposes the DSH profile and an honest capability
  boundary. Unsupported queue/cancel/steer/retry/fork/rename/archive features
  are not rendered as fake controls.
- AISummoner Server remains the only User, Device, Session, approval and Remote
  capability authority. No DSH backend, credential store, local shell or local
  filesystem authority was imported.
- `THIRD_PARTY_NOTICES.md` records the DSH MIT attribution and pinned source.

## Main Files Changed

- `web/src/App.tsx`, `App.test.tsx`, `components/AppShell.tsx` — full-viewport
  Workspace and legacy Device route migration.
- `web/src/pages/WorkspacePage.tsx`, `WorkspacePage.test.tsx`,
  `workspace/SessionRail.tsx`, `workspace/WorkspaceDock.tsx` — DSH shell,
  integrated Settings and retained Task017 lifecycle behavior.
- `web/src/pages/AgentPage.tsx`, `AgentPage.test.tsx`,
  `agent/ReasoningBlock.tsx`, `agent/ToolCallCard.tsx` — DSH conversation,
  separated reasoning/tool/output and floating composer.
- `web/src/components/ControllerSettingsDialog.tsx` and test — unified
  Settings; `components/Icons.tsx` — accessible icon system.
- `web/src/agent/experience.ts` and test — pinned experience/capability seam.
- `web/src/pages/LoginPage.tsx`, `DevicesPage.tsx` and tests — coherent entry
  surfaces.
- `web/src/styles.css` — source-aligned DSH light presentation and preserved
  responsive/accessibility rules.
- Deleted `web/src/pages/DevicePage.tsx` and
  `web/src/agent/DeepSeekSetupDialog.tsx`.
- `web/vite.config.ts` — isolated preview proxy override.
- ADR-0005, baseline 04, shared context/roadmap/todo/state and third-party
  notice — durable product direction.

## Verification

- Final `npm test -- --run` — PASS: 16 files, 66 tests.
- Final `NODE_OPTIONS=--max-old-space-size=2048 npm run build` — PASS:
  TypeScript and Vite, 73 modules; CSS 37.49 kB, JS 592.00 kB.
- `git diff --check` — PASS.
- Playwright visual smoke — PASS at 1440×960 for active Agent and Settings,
  and at 1024×768 for the explicit single-panel fallback. Screenshots confirmed
  a bottom-seated composer, visible Agent navigation and no legacy Manage UI.
- A local isolated preview is running at
  `http://127.0.0.1:4173/devices/dev_online/workspace`; its API data is a
  non-secret presentation mock on `127.0.0.1:18080`, not a product backend.

## Honest Failure And Retry Record

1. The first post-refactor suite passed 50/62. Nine Workspace fixtures lacked
   an Auth provider because Settings authentication was read by the parent even
   while closed; two Agent fixtures still expected the retired setup dialog;
   one query matched duplicate visible metadata. Authentication was moved into
   the conditionally mounted Settings child and tests were rebaselined to the
   new product surface.
2. The next full suite passed 65/65. Adding a production-root Settings/unpair
   regression exposed one test-only unscoped Device-ID query; scoping it to the
   dialog produced the final 66/66 result.
3. The first build after adding the preview override failed because the Web
   tsconfig intentionally does not expose Node's global `process`. The config
   now uses Vite `loadEnv`, preserving that boundary; the final build passed.
4. Browser smoke after removing the non-DSH runtime banner exposed CSS Grid
   auto-placement moving the composer into the middle row. The Agent workspace
   now uses an explicit flex column with a flexing conversation, and repeated
   desktop/narrow screenshots show the composer at the bottom.

## Known Follow-Up

- The existing Vite warning for the approximately 592 kB main chunk remains;
  route/vendor splitting is a later performance task, not a correctness issue.
- Task018 completes the DSH presentation baseline, not the DSH Runtime. Native
  long-session events, cancel/steer/queue/retry, richer Markdown/diff and
  Runtime integration remain the next Agent-domain slice.
- Independent review is intentionally not fabricated. This revision is frozen
  for the user's visual review as explicitly requested without sub-Agent work.
