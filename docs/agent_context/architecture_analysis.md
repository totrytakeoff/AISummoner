---
type: architecture_analysis
status: draft
updated_by: planner
review_required: true
---

# Architecture Analysis

## Current Shape

The current tree is a proved MVP vertical slice, not a mature two-client
product. The Go Server is the authoritative control plane and the Go Remote
Client supplies outbound Tunnel/SSH execution. The Browser has separate
Device, Terminal and Agent pages. Agent sessions, approvals and ordered events
are already Server-owned; Fake, OpenCode and direct DeepSeek use the same
Remote execution boundary.

The strongest reusable assets are the trust chain, owner-scoped persistence,
Tunnel/SSH lifecycle and Terminal data plane. The primary debt is above that
line: fragmented Controller information architecture, a minimal one-Turn
Adapter interface, incomplete Runtime capabilities, and a CLI-only Remote user
experience.

## Alpha Target Shape

```text
Controller
  ├─ Login / Device Hub
  └─ Device-scoped Control Workspace
      ├─ Session rail
      ├─ Agent Conversation
      └─ optional dock: Terminal / Activity / future Desktop
              │ REST + normalized SSE + WebSocket
              ▼
Go Server (single product authority)
  ├─ auth / owner / pairing / audit
  ├─ Agent Session Log + Capability Descriptor
  ├─ Runtime supervisor and adapter registry
  │   ├─ direct DeepSeek
  │   ├─ DSH sidecar adapter
  │   ├─ OpenCode adapter
  │   ├─ Codex App Server adapter
  │   └─ Claude Agent SDK adapter
  ├─ Remote Capability Bridge
  └─ Tunnel Manager / strict SSH client
              │ WSS / yamux / SSH
              ▼
Remote Core Daemon
  ├─ Device identity / reconnect / Embedded SSHD
  ├─ local restrictive policy
  ├─ bounded sanitized activity events
  └─ private Unix socket
              ▲
       Remote Desktop UI
```

## Key Decisions

- Keep the Go Server as the only user/Device/Session/approval authority.
- Preserve the existing WSS→yamux→strict SSH execution plane; do not rewrite it
  as part of UI work.
- Replace separate Terminal/Agent navigation with one Device-scoped workspace.
- Use an ordered normalized Session Log to drive every Runtime through one UI.
- Split Agent integration into Runtime Adapter, Capability Descriptor, Remote
  Capability Bridge, and Provider/Tool presentation.
- Use DSH as the first rich interaction/runtime reference without importing its
  Host, database, credentials or local execution authority.
- Implement rich Runtime support in order DSH→OpenCode→Codex→Claude Code;
  retain direct DeepSeek as a lightweight model API Adapter.
- Prefer official machine interfaces: Codex App Server over TUI scraping and
  Claude Agent SDK streaming input over ANSI/CLI parsing.
- Separate the Remote Go daemon from the Desktop UI over private local IPC.
- Start with a fixed three-zone resizable layout; arbitrary IDE docking is a
  later optimization, not an Alpha prerequisite.

## Required New Seams

### Agent Runtime Contract

The current single `Run` call evolves behind compatibility shims toward
`Describe/Health`, native Session create/resume/close, Turn start/steer/cancel,
normalized event streaming and interaction resolution. A Capability Descriptor
allows the UI to expose only supported actions.

### Session Index

The Controller needs an owner-scoped, bounded Session summary index. The list
cannot return entire transcripts or force N snapshot queries. Existing latest
Session and snapshot endpoints remain compatible during migration.

### Runtime Supervision

Each sidecar/child protocol needs explicit startup health, version pinning,
bounded streams, cancellation, joined shutdown, secret injection and per-Session
mapping. Runtime crashes must become safe Session errors rather than Server
process failure.

### Remote Capability Plane

`remote_exec` remains the first tool, but native coding workflows need bounded
file read/search/write/patch/diff. These operations must be implemented as
Device-bound capabilities and must never fall through to the Server filesystem.

### Remote Local IPC

The GUI needs typed status/pairing/pause/event/policy calls over a mode-0600
Unix socket with peer UID validation. Device private key operations remain in
the daemon. GUI exit and daemon disconnect are distinct state transitions.

## Migration Strategy

1. Split Remote CLI internals into a daemon controller and private IPC, then
   add the Qt 6 Widgets GUI/AppImage while retaining headless mode.
2. Add the Controller workspace shell and bounded Session index while embedding
   the current Agent/Terminal behavior as reusable surfaces.
3. Introduce event v2/capabilities behind translation from existing events;
   migrate the UI to DSH-like nodes and add the DSH Adapter.
4. Enrich OpenCode, then add Codex and Claude using the same contracts.
5. Add structured file/diff and Desktop capabilities only through separate ADRs.

Old routes should redirect for at least one migration release. Existing Session
rows must remain readable; schema changes are forward-only and must have a
bounded compatibility period.

## Risks And Tradeoffs

- **Lowest-common-denominator UI**: one generic Adapter can erase useful native
  features. Mitigate with explicit capability descriptors and optional standard
  event families, not Provider-name conditionals.
- **Two conversation authorities**: native Runtime history can conflict with
  AISummoner history. Treat native IDs as recovery handles only; persist product
  events before exposing them.
- **Server-local execution escape**: coding runtimes often assume a local
  workspace. Disable those tools and inject Remote capability providers; isolate
  sidecars in empty workspaces.
- **DSH churn**: it is pre-1.0. Pin the version, test public seams and avoid
  internal package coupling.
- **Layout rewrite scope**: arbitrary docking can consume the roadmap. Deliver
  accessible fixed split panels first.
- **Desktop packaging**: Qt platform plugins and system ABI dependencies differ
  across Ubuntu versions. Keep daemon/IPC toolkit-neutral, use Qt Widgets rather
  than WebEngine, and require AppImage tests on the oldest supported Ubuntu.
- **Credential proliferation**: multiple runtimes introduce API keys/OAuth.
  Keep secrets server-side, never return them, and design encrypted/reference
  storage before replacing the current memory-only setup.
- **Event growth**: replayable Session logs can grow without limit. Add cursor,
  snapshot/compaction and byte policies before long-running sessions become the
  default.

## Review Questions

- Can deleting any Runtime-specific code leave owner/approval/Device binding
  intact and the UI still render a safe fallback?
- Does Device or Session switching cancel every old request/SSE/WebSocket before
  the new context becomes visible?
- Can any sidecar access the Server host or choose a Device without going
  through the capability bridge?
- Are unsupported Runtime actions capability-gated rather than guessed from a
  provider name?
- Does closing the Remote GUI preserve the daemon, while manual Pause joinedly
  closes Tunnel children and prevents reconnect?
- Can legacy Sessions/routes survive each migration phase and roll back without
  data loss?
