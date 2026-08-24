---
task_id: task020
type: plan
status: in_progress
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 020 Plan: DSH-First Agent Runtime

## Goal

Make the pinned DeepSeek Harness checkout the first-class Agent runtime behind
the accepted Controller experience, then prove the complete Browser →
AISummoner → DSH → approved Remote capability → SSH → Device chain and deploy
that build for human testing.

The integration uses DSH commit
`47f943859bef60e4160492346772ded9b24f765a` under its MIT licence. DSH remains a
private, supervised loopback sidecar; AISummoner remains the sole authority for
users, Device ownership, product Sessions, approval, audit and Remote execution.

## Required Changes

### 1. Pinned private DSH runtime

- Package or launch only the pinned DSH CLI/runtime artifacts; no floating npm
  version and no Browser-direct DSH endpoint.
- Supervise one loopback-only DSH Host process with bounded startup, shutdown,
  cancellation and crash classification. Its private home, credential store and
  AISummoner preset must be created with private permissions.
- Provision a dedicated `aisummoner` DSH preset which disables DSH local shell,
  filesystem, search, subagent and workflow tools. It exposes exactly one
  replacement tool backed by the AISummoner Remote capability bridge.
- Never log credentials, prompts, command text, tool output, Runtime event
  payloads or private paths.

### 2. DSH Runtime Adapter

- Add a provider-neutral Adapter implementation backed by DSH Host RPC and its
  multiplexed Session event stream.
- Create or resume the DSH Session using the AISummoner-persisted external
  Session ID and the fixed `aisummoner` preset.
- Map DSH reasoning/text deltas and terminal Turn status into existing ordered
  AISummoner events without exposing raw provider errors.
- On product Turn cancellation, issue bounded DSH `session.cancel`, close the
  stream and join all owned workers. A Runtime crash must fail only the Turn and
  must not terminate the Server.
- DSH owns its native rich transcript while AISummoner continues to persist the
  bounded user/assistant projection required for Controller replay and provider
  independence.

### 3. Remote-only DSH tool bridge

- Reuse or generalize the existing HMAC loopback capability bridge with a DSH-
  specific path and proof domain; preserve OpenCode behavior.
- Bind each callback to the exact active AISummoner Turn and exact external DSH
  Session ID. The tool must not accept a Device ID from DSH.
- Route every command through the existing `RemoteExecInvoker`, so approval,
  online/owner rechecks, output bounds, timeout and SSH identity remain product-
  owned.
- Reject stale, replayed, concurrent or malformed callbacks without leaking
  payloads. Bridge shutdown must join active callbacks.

### 4. Server configuration and credential control

- Add exact `dsh` configuration with literal-loopback Host and bridge endpoints,
  an absolute pinned CLI entrypoint, a private DSH home and the shared bridge
  secret. Fake mode must continue to ignore all DSH-only settings.
- Start and probe DSH before public readiness. Startup failure must unwind every
  private listener/process and leave the database under the existing DB-last
  Runtime ownership.
- Add an authenticated same-origin DSH credential endpoint. It writes the
  DeepSeek key only to DSH's private credential API/store, never returns it and
  never stores it in Browser storage, AISummoner SQLite, logs or audit fields.
- New Agent Sessions use provider `dsh`; previously persisted Fake/OpenCode/
  direct-DeepSeek Sessions retain their original provider binding.

### 5. DSH-first Controller settings

- Replace the temporary direct-DeepSeek setup action with DSH Runtime setup and
  report the actual active Runtime accurately.
- Preserve the accepted DSH interaction model and Chinese UI. Do not add an
  iframe, a second login, a DSH-specific page or a provider-owned tool approval
  surface.
- The settings copy must accurately state that the key is stored in the private
  Server-side DSH credential store and can be replaced, but is never returned.

### 6. Packaging and deployment

- Add a reproducible pinned DSH runtime packaging path from the local source
  checkout and record its source commit/licence in third-party notices.
- Update development/deployment configuration without exposing the DSH Host or
  capability bridge publicly.
- Deploy the verified Server build to the existing bounded ASD test topology,
  preserving all unrelated services and listeners, then run the real DSH Turn
  through the already paired Remote Client.

## Required Verification

- Fixed protocol fixtures for DSH RPC envelopes, event ordering, create/resume,
  reasoning/final separation, terminal success/failure and malformed frames.
- Deterministic cancellation and sidecar-exit tests proving no leaked stream,
  callback, process or WaitGroup worker.
- Tool-bridge tests proving exact Session/Turn binding, approval, replay and
  stale callback denial, output bounds and no Server-local command execution.
- Config matrices for Fake isolation and all DSH loopback/private-path/secret
  requirements; startup unwind and Runtime shutdown ordering tests.
- Agent API and Controller tests for authenticated DSH credential setup, origin,
  body bounds, redaction and provider switching for new Sessions only.
- Full focused Go tests/race/vet/build and Web Vitest/build; pinned DSH package
  build/protocol smoke.
- One real deployment E2E: login → Device Workspace → DSH Session → streamed
  reasoning/final → approved Remote tool → result → Session resume/cancel.

## Out Of Scope

- Rich OpenCode, Codex or Claude Code adapters.
- Browser access to DSH Host or DSH's own Web application.
- A second Device/session/approval authority, Server-local model tools, or
  provider-selected Device IDs.
- Credential-at-rest encryption beyond DSH's existing private per-user store;
  the long-term external secret-reference design remains later Alpha work.
- Unrelated Remote Qt Client or Controller component development.

## Acceptance

Task020 is complete only when the Controller visibly runs a real resumable DSH
Session, DSH can execute solely through the owned Remote Device after the normal
AISummoner approval path, cancellation and shutdown are joined, credentials and
payloads remain private, and the verified build is available in the bounded ASD
test deployment without disturbing existing services.
