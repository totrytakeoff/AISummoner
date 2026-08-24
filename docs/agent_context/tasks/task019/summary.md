---
task_id: task019
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 019 Summary: Single Component Entry And Chinese Controller

## Outcome

The accepted DSH-first Controller baseline now has one unambiguous component
launcher and a complete Simplified-Chinese product shell. The Workspace toolbar
no longer repeats Device and Terminal actions: it exposes one `组件` button,
while the opened dock owns the `终端` and `设备` tabs and remembers the active
tab. Product/runtime names and real Agent or command content remain unchanged.

## Behavior

- One toolbar action opens or closes the component dock. There is no separate
  toolbar Device or Terminal action.
- The dock alone switches Terminal and Device details, retains maximize/close
  behavior, releases the Terminal on close/Session/Device change, and restores
  focus to the component launcher.
- `?dock=terminal` and `?dock=activity` remain supported. On <=1259 px they now
  select the visible component panel immediately instead of leaving the deep
  link mounted behind the Agent panel.
- Login, Device Hub, Session rail, Workspace chrome, Settings, Agent states,
  reasoning/tool disclosure, approval controls, Terminal states, confirmations
  and accessibility names are Chinese.
- Standard Server error codes are localized at the Browser boundary. Status,
  machine code and request ID remain available to program logic, while raw
  unknown server text is not exposed as English UI.

## Main Files

- `web/src/pages/WorkspacePage.tsx`, `workspace/WorkspaceDock.tsx` and
  `components/Icons.tsx` — single component launcher, internal tabs and narrow
  deep-link visibility.
- `web/src/api/client.ts` — safe Chinese error projection.
- `web/src/pages`, `web/src/components`, `web/src/workspace`,
  `web/src/agent`, `web/src/terminal` — Chinese static copy, status and
  accessible labels.
- Existing focused tests were rebaselined to the Chinese contract.
  `WorkspacePage.test.tsx` now explicitly proves that the toolbar has no
  Device/Terminal actions and that the dock owns both tabs.

## Verification

- `npm test -- --run` — PASS: 16 files, 66 tests.
- `npm run build` from `web/` — PASS: TypeScript and Vite, 73 transformed
  modules; CSS 37.49 kB, JS 594.35 kB.
- `git diff --check` — PASS.
- Playwright visual smoke — PASS at 1440×900 and 1024×768 with
  `?dock=activity`; both screenshots were inspected at original resolution.

## Honest Failure Record

1. The first localized suite passed 22/66 because the unchanged tests still
   queried the retired English contract. Production output showed the expected
   Chinese labels. Tests were updated without weakening their behavioral
   assertions; the final suite passed 66/66.
2. The first build invocation ran from the repository root, which has no
   `package.json`, and failed before any build action. Re-running the intended
   command from `web/` passed.
3. The first 1024px screenshot waited for the intentionally hidden Agent panel
   while the deep link correctly selected Components. That owned Playwright
   process was stopped and the screenshot was repeated against the visible
   component dock.

## Follow-Up

Task019 closes only the two requested UI findings. The next slice is the real
DSH Runtime integration: preserve the DSH interaction model as the primary
Agent experience while keeping AISummoner Server as the authority for users,
devices, permissions and the Remote execution capability.
