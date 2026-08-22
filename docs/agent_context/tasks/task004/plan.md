---
task_id: task004
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 004 Plan: React WebUI Product Surface

## Status

Ready for implementation. This task is intentionally independent from the unfinished Go Terminal and Agent handlers: it consumes the frozen API contracts and must not edit backend files.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

Task001 provides login, current-user, pairing and device APIs. Tasks005-007 will provide the Terminal WebSocket and Agent REST/SSE endpoints already frozen in `docs/baseline/02-protocol-data-security.md`.

## Goal

Build a coherent React/TypeScript/Vite WebUI that covers the complete MVP user flow: login, pair, device list/detail, interactive xterm terminal, Agent session/chat/event stream, command approval and session-only Full Access confirmation.

## Relevant Files

- `web/`
- root `.gitignore` only for Web build/test artifacts if required
- root `Makefile` only for Web convenience targets if required
- `docs/agent_context/tasks/task004/summary.md`

Do not modify `cmd/`, `internal/`, `migrations/`, baseline documents or ADRs.

## Required Behavior

- Use React, TypeScript, Vite and React Router with a small, typed same-origin API client.
- Bootstrap authentication with `GET /api/v1/me`; redirect unauthenticated users to login and return authenticated users to devices.
- Login posts username/password, handles the standard error envelope without leaking credentials, and relies on the HttpOnly cookie.
- Device page lists name, platform, architecture, client version, last seen and clear Online/Offline state; poll or refresh often enough to show heartbeat changes.
- Pair form accepts the human code, normalizes user-friendly spacing/case, shows invalid/expired/already-used errors, and refreshes the selected/listed device after success.
- Device detail exposes Terminal and Agent actions only when Online and supports explicit unpair confirmation.
- Terminal page uses `@xterm/xterm` plus fit addon, sends binary input, renders binary output, and sends bounded JSON `terminal.resize` messages after fitting. It must close the WebSocket and observers on unmount and display offline/close/error states.
- Agent page creates a session with `per_command` by default. `full_access` requires an explicit confirmation immediately before creation and is visually scoped to that one session.
- Agent message form posts one turn at a time. EventSource consumes the frozen SSE event names, incrementally renders text, tool command/status/output/exit code, completion and failure states, and cleans up on unmount.
- Pending tool calls expose approve-once, approve-session and deny. Approval controls disable after a decision and errors remain recoverable.
- Use accessible labels, keyboard-reachable actions, visible focus, and responsive layout. Avoid animation/theme work outside MVP.
- Never persist password, cookie, terminal input, tool bridge token or other secret in localStorage/sessionStorage/logs.

## API Contract Assumptions

- Existing Task001 response shapes in `internal/httpapi/responses.go` are authoritative for auth/devices.
- Terminal endpoint: `GET /api/v1/devices/{device_id}/terminal` WebSocket; binary data plus text resize frames.
- Agent endpoints and event names are exactly those frozen in baseline section 5.4.
- New Agent response/event payloads should be parsed defensively: required identity/status fields are typed while unknown fields are retained/ignored safely.
- All requests are same-origin with cookie credentials. A 401 globally returns to login; API errors use `{error:{code,message,request_id}}`.

## Verification

```bash
npm --prefix web ci
npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Run Vitest without browser workers beyond the default lightweight pool. Do not run Playwright in this task.

## Required Tests

- API error-envelope parsing and 401 behavior.
- Login success/failure.
- Device list online/offline and successful pairing refresh.
- Terminal binary/control behavior and cleanup with a mock WebSocket/xterm boundary.
- Agent text/tool event projection and all three approval decisions.
- Full Access cannot be created without explicit confirmation.

## Documentation Requirements

- Write `docs/agent_context/tasks/task004/summary.md` with exact install/test/build results and any assumed payload fields.

## Out Of Scope

- Implementing or changing Go endpoints.
- Static embedding, Docker, Caddy and production asset serving.
- Terminal reconnect/history, file UI, desktop, port forwarding or multi-user administration.
- Playwright/full three-host E2E.

## Acceptance Criteria

The task is ready for review when:

- The complete user flow is navigable with mock or real same-origin endpoints.
- Terminal and Agent transports are cleaned up correctly.
- Approval and Full Access UI semantics match the frozen security model.
- Tests and production Web build pass within the memory limit.
- No backend or baseline file was changed.
