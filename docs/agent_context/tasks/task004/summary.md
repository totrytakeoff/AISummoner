---
task_id: task004
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 1
review_required: true
---

# Task 004 Summary: React WebUI Product Surface

## Files Changed

- `web/package.json`, `web/package-lock.json`, `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html` - reproducible React 19/TypeScript/Vite/Vitest scaffold, same-origin development proxy, production build, and Node tests.
- `web/src/api/` - typed same-origin cookie API client, standard error-envelope parsing, global 401 transition, pairing-code normalization, Terminal WebSocket URL and Agent SSE URL helpers.
- `web/src/auth/` and `web/src/App.tsx` - `/api/v1/me` authentication bootstrap, protected/public routing, login and logout state.
- `web/src/pages/`, `web/src/components/`, `web/src/devices/` - login, pairing, route-safe polled device detail, Online/Offline state, reusable keyboard-safe confirmation dialogs, explicit unpair confirmation, and online-gated Terminal/Agent actions.
- `web/src/terminal/` - xterm.js + fit addon transport boundary with binary input/output, bounded resize controls, connection states, frame chunking, and complete WebSocket/observer/xterm cleanup.
- `web/src/agent/` - defensive named-SSE event parser/projector, stream-readiness gate, session-keyed lifecycle reducer, monotonic optimistic message submission, Task006 tool-result fields, three approval decisions, recoverable approval errors, and complete EventSource listener cleanup.
- `web/src/pages/AgentPage.tsx` - per-command default, explicit session-scoped Full Access confirmation, device-bound state reset, one-turn-at-a-time composer, Agent activity and failure states.
- `web/src/styles.css` - responsive MVP layout, visible focus, accessible status/error surfaces, terminal and Agent workspaces without animation/theme infrastructure.
- `web/src/**/*.test.*`, `web/src/test/` - focused contract, UI, transport and security-semantics tests.

## Behavior Changed

- Authenticated users can navigate the entire Web flow from login through pairing, device detail, Terminal and Agent; a 401 globally returns the application to login.
- Devices refresh every five seconds, expose explicit Online/Offline state, and never expose Terminal or Agent actions while offline.
- Pairing input accepts spacing/case variants, sends normalized `XXXX-XXXX`, reports server invalid/expired/used errors, and refreshes devices after success.
- The Terminal writes only binary shell output, encodes keyboard input as binary chunks no larger than 64 KiB, clamps resize to 1-500 columns and 1-300 rows, and releases every owned resource on unmount.
- Agent sessions default to `per_command`. `full_access` is not sent until the user confirms an immediately preceding alert dialog; `approve_session` also updates the visible current-session scope.
- Agent messages are disabled until EventSource reports `open`, are inserted before the POST can race with SSE, and are confirmed without changing a terminal turn state. A rejected POST rolls back only when no server turn event was observed.
- Agent text/tool events are projected incrementally and reset across session/device identity changes. Pending commands expose `approve_once`, `approve_session`, and `deny`; controls disappear after a successful decision and stay retryable after an error.
- Full Access and unpair confirmations start on the safe Cancel action, contain Tab/Shift+Tab focus, close on Escape while idle, and restore focus to their trigger.
- No password, terminal input, cookie, bridge token or other secret is written to browser storage or console output.

## Verification

- Memory gate before revision verification: `MemAvailable: 14340312 kB`, `SwapFree: 9747700 kB`, above the required 8 GiB/4 GiB thresholds.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web ci`
  Result: PASS in revision 0; installed 167 packages, audited 168 packages, 0 vulnerabilities. It was not rerun in revision 1 because neither dependency manifest changed. npm reported that the `esbuild@0.28.2` postinstall was blocked by npm's script policy, but its platform package was available and both tests and Vite build completed successfully.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run`
  Result: PASS; 8 test files and 22 tests passed in 1.15 s. Coverage includes error/401 behavior, login success/failure, pairing refresh, Online/Offline devices, Terminal binary/resize/frame-boundary/full-cleanup behavior, Agent projection/readiness/SSE-before-POST/session replacement/listener cleanup, successful and failed/retried tool decisions, device-session isolation, and Full Access modal keyboard/focus behavior.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build`
  Result: PASS; TypeScript check and Vite 7.3.6 production build completed in 1.06 s. Output was 0.49 kB HTML, 14.26 kB CSS and 557.23 kB JS (155.90 kB gzip). Vite emitted a non-failing warning that the xterm-containing JS chunk exceeds 500 kB.
- Memory after verification: `MemAvailable: 13994096 kB`, `SwapFree: 9747888 kB`; no memory-pressure event occurred.

## Deviations From Plan

- Tailwind/shadcn were not added. The frozen task prioritizes complete behavior and explicitly excludes theme work; a small repository-owned CSS layer avoids additional build/runtime dependencies while retaining the required responsive and accessible surface.
- Playwright was not run, as explicitly required by task004.
- No root `.gitignore` or `Makefile` change was necessary because the existing files already contain Web artifact exclusions and Web convenience targets are not needed for the required commands.

## Known Issues / Follow-Up

- Agent endpoint response assumptions for task006: session creation returns `{ "session": { "id", "device_id", "approval_mode", "state", ... } }`; message submission accepts `{ "content": string }`; tool decisions accept `{ "decision": "approve_once" | "approve_session" | "deny" }`. Message/decision response bodies are intentionally ignored.
- Named SSE events use the frozen event names. The UI accepts `payload`, `data`, or top-level payload fields and recognizes text fields `delta|text|content`; tool IDs `tool_call_id|id|call_id`; arguments `arguments|arguments_json|input`; output `output|output_excerpt|delta|text|content|stdout|stderr`; and Task006 `truncated`, `denied`, nested `failure`, status and exit fields. Task006 should emit the canonical first-listed names.
- Native EventSource does not expose an HTTP 401 status to JavaScript; an unauthorized Agent stream appears as a recoverable reconnecting error until the page's five-second Device REST poll triggers the global login transition.
- Production JS has a 557.23 kB chunk because xterm is eagerly loaded. Route-level lazy loading can remove the warning after MVP integration; it does not block functionality or the memory budget.
- Real-browser xterm rendering and full three-host behavior remain for task005/task010 and were not claimed here.

## Revision 1

### Changes

- Added a session-keyed Agent reducer and explicit EventSource `connecting/open/error` state. The composer is unavailable before `open`; old-session events and actions are ignored; all open/error/named listeners are removed on replacement or unmount.
- Added optimistic user-message transactions. The user message is ordered before any possible SSE response, POST success only clears transaction metadata, and POST failure restores the previous view/prompt only when no server turn event has begun. A late response cannot regress `turn.completed` or `turn.failed`.
- Bound Agent Session/approval state to the current route Device and made `useDevice` discard stale/out-of-order requests, so route changes fail closed without showing or using the previous Device's Full Access state.
- Added reusable `ConfirmDialog` focus management and applied it to Full Access and unpair actions.
- Added canonical Task006 combined stdout/stderr, truncation, denial and structured failure projection.
- Expanded deterministic regressions from 16 to 22 tests: pre-open send gating, SSE completion before delayed POST response, optimistic rejection rollback, session/device isolation, modal focus loop/Escape/restore, approval failure then retry, named SSE teardown/replacement/recovery, Terminal frame chunking/clamping and complete teardown.

### Verification

- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm test -- --run` (first revision run)
  Result: FAIL; 20 passed and 2 new AgentPage tests exposed jsdom's missing `scrollIntoView`. The call is now capability-checked.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm test -- --run src/pages/AgentPage.test.tsx` (first focused rerun)
  Result: FAIL; 4 passed and 1 assertion matched the restored textarea value instead of a chat message. The assertion was corrected to inspect the user-message surface.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm run build` (first revision build)
  Result: FAIL; TypeScript rejected nullable `ScopedSession` narrowing. Explicit null/device/session guards fixed the errors.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm test -- --run` (final)
  Result: PASS; 8 files and 22 tests passed in 1.15 s.
- Command: `NODE_OPTIONS=--max-old-space-size=2048 npm run build` (final)
  Result: PASS; TypeScript and Vite build completed in 1.06 s with only the documented 557.23 kB chunk warning.

### Remaining Issues

- No revision-specific functional issue remains. The documented EventSource status limitation, eager xterm chunk warning, and deferred real-browser/three-host verification remain unchanged in scope.
