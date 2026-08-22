---
task_id: task013
type: plan
status: in_progress
from: orchestrator
to: implementation
revision: 0
requires_review: true
---

# Task 013 Plan: DSH-Inspired OpenCode Conversation UX And Turn Reliability

## Goal

Make the real OpenCode Agent usable as a continuous, native conversation:
re-entering the Agent page resumes the latest owned Device session, provider
reasoning is visually and semantically separate from the final answer, and a
provider failure is classified instead of being reported as an unexplained
`agent turn failed`.

## User Direction And Live Reproduction

- Use `/home/myself/workspace/deepseek-harness` as the primary interaction
  reference, especially its durable session selection, ordered conversation
  nodes, collapsed reasoning row and composer-takeover approval flow.
- Prove one real OpenCode integration before adding other Agent providers, but
  keep the normalized invocation and presentation adapter seams required by
  ADR-0003.
- Do not expose DSH's own host, local subprocess, filesystem or plugin backend
  on the AISummoner Server. Browser requests, approval and execution must stay
  behind AISummoner auth, Device ownership, Tunnel/SSH and `remote_exec`.
- A bounded live probe reproduced the current failure without printing prompt
  or provider content: the first no-tool message was prematurely reported as
  `turn.completed`; a second message in the same product Session failed with
  `protocol_error`. The provider sequence was assistant placeholder, idle, then
  `session.error`; OpenCode had not emitted a final assistant finish marker.

## Implementation Scope

### 1. Provider Completion And Failure Semantics

- Extend the OpenCode event tracker to distinguish an assistant placeholder,
  intermediate tool-call finishes and a genuinely final assistant finish.
- Treat `session.status=idle` or `session.idle` as terminal success only after a
  non-tool final assistant finish. Remember an early idle and complete if the
  final update arrives afterward.
- Continue reading after an idle without a final finish so the following
  `session.error` is classified. Preserve current-turn parent correlation,
  event bounds, late Bridge failure ownership and cancellation joins.
- Map safe provider failure codes to actionable Browser messages without
  exposing provider bodies, credentials, prompts or command output.

### 2. Reasoning As A First-Class Normalized Event

- Normalize OpenCode reasoning parts into bounded
  `response.reasoning.delta` / `response.reasoning.done` domain events. Browser
  code must not parse OpenCode-native part types.
- Keep reasoning and assistant answer buffers separate in the Agent Service.
  Persist reasoning as a distinct owned transcript message so reload/resume
  does not merge it into final output.
- Render reasoning as a collapsed `Think` disclosure with a short summary;
  assistant output remains a normal message. Tool calls retain their original
  ordered timeline position.

### 3. Durable Session Resume And Explicit New Conversation

- Add an owner-checked latest-session lookup for a Device using the canonical
  Device/session ownership predicate and excluding revoked sessions.
- `GET /api/v1/devices/{device_id}/agent-sessions` returns the latest owned
  snapshot; absence is a normal no-session result. `POST` continues to create
  an explicitly selected approval-mode session.
- On Agent page entry, load and render that snapshot automatically. Do not ask
  for approval mode again merely because the route remounted or the Browser
  refreshed.
- Provide an explicit `New conversation` action that returns to the mode
  chooser. Full Access remains scoped to one newly created Session and still
  requires confirmation.

### 4. DSH-Inspired Presentation Layer

- Adapt the proven DSH interaction responsibilities, not its Cordis runtime:
  server-owned transcript facts, Browser projection, one stable timeline,
  collapsible reasoning and approval replacing the composer.
- Keep provider-specific naming/capabilities in the existing presentation
  adapter. OpenCode is the first complete adapter; Fake and unknown providers
  retain honest fallbacks.
- If source code is copied rather than independently implemented, add the MIT
  attribution and precise file provenance in the same change.

## Security And Compatibility Constraints

- No Browser-to-OpenCode connection and no Provider credentials in Web assets.
- No Server-local shell, DSH BFF, DSH subprocess provider or second execution
  authority.
- Every resume/snapshot path rechecks the authenticated user against current
  Device ownership; unpair/revocation continues to hide old sessions.
- Preserve per-command approval, Session Full Access scope, SSH host-key
  validation, command/output/time/tool-count limits and joined shutdown.
- Reasoning and provider error bodies are not written to application logs.

## Verification

- Deterministic OpenCode event tests for placeholder-idle-error, early-idle then
  final, final-before-idle, tool-call intermediate finish, reasoning/text
  separation, duplicate families and current-turn correlation.
- Store/Service/API tests for latest owned snapshot, no-session, cross-owner,
  revoked/unpaired and concurrent Turn behavior.
- Web tests for automatic resume, explicit New conversation, one-time mode
  selection, snapshot hydration, collapsed reasoning versus final output,
  actionable failures, ordered tools and approval takeover.
- Relevant Go package tests and races; full Web Vitest and production build;
  final static Server build from the exact Web dist.
- Bounded live Browser/API checks on the existing scoped Task011 deployment:
  two consecutive messages in one OpenCode Session, reasoning separated from
  final output, route leave/re-entry resumes without a mode prompt, and a
  provider error is not falsely reported as success.

## Deployment And Rollback

- Keep Caddy TCP 10001, nginx/TCP 80, Remote Client, SQLite and unrelated ASD
  containers untouched.
- Change only the scoped Task011 Server binary/Web assets and existing
  loopback OpenCode integration. Preserve an exact pre-Task013 binary rollback
  and environment hash before restart.
- Fail back to the Task012 Server binary if readiness, owner checks, Terminal,
  Agent SSE or OpenCode health regresses.

## Out Of Scope

- Running or publicly exposing the full DSH backend.
- Adding a second real Agent provider in this task.
- DSH workspaces, local shell, filesystem, skills, subagents, attachments,
  model settings or plugin marketplace.
- Changing public TLS, Device pairing, Tunnel protocol, SSH trust or Terminal.
