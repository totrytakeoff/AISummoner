---
type: architecture_analysis
status: accepted
updated_by: task014_implementation
review_required: true
---

# Architecture Analysis

## Current Shape

The repository contains the integrated and independently reviewed foundation, Device Tunnel, Embedded SSH/PTTY, Terminal, Agent Core, WebUI, OpenCode bridge, lifecycle and deployment work. Task014 adds a direct DeepSeek Adapter and native conversation interaction while retaining the same Server-owned authorization, approval, transcript and remote-execution boundaries.

## Target Shape

```text
Browser (local)
  ├─ REST: auth, pairing, devices, agent commands
  ├─ WebSocket: xterm terminal bytes and resize
  └─ SSE: product-level Agent events
          ↓
Go Server (ASD-Host)
  ├─ SQLite auth/device/session/audit state
  ├─ WSS/yamux connection manager
  ├─ SSH client terminal/exec gateway
  ├─ Agent orchestrator + approval gate + durable transcript
  └─ provider adapter
      ├─ direct DeepSeek HTTPS/SSE
      └─ loopback OpenCode HTTP/SSE + custom-tool bridge
          ↓ WSS/yamux/SSH
Go Remote Client (lzr-host)
  └─ Ed25519 identity, heartbeat/reconnect, Embedded SSHD, PTY/exec
```

## Key Decisions

- Decision: Keep one Go module and single Server executable.
  Rationale: Lowest integration and deployment cost for the MVP.
- Decision: Public transport is WSS, logical streams are yamux, remote semantics are SSH.
  Rationale: NAT-friendly outbound connection without reimplementing terminal semantics.
- Decision: OpenCode is a loopback sidecar with all local tools disabled.
  Rationale: Reuse the Agent Loop while preserving Remote as the only execution plane.
- Decision: OpenCode custom tool calls an authenticated Go bridge.
  Rationale: Approval and SSH access remain inside the trusted Server and the model cannot choose a host.
- Decision: Fake Adapter is first-class test infrastructure.
  Rationale: Free model availability and rate limits are nondeterministic.
- Decision: Direct providers and OpenCode share one `agent.Adapter` and one ordered Web timeline.
  Rationale: DSH-like interaction quality is useful, but AISummoner must remain the only owner of Device selection, approval, SSH execution and persisted conversation state.
- Decision: Interactive DeepSeek credentials enter through an authenticated same-origin Web form and live only in the Server process.
  Rationale: Testing should not require shell access, while SQLite, logs, Browser storage and Provider-direct Browser calls remain outside the credential boundary.
- Decision: SQLite WAL and in-memory Connection Manager.
  Rationale: Single-node MVP; no Redis/Postgres or cluster requirements.

## Risks And Tradeoffs

- Risk: Agent executes on Server through a forgotten built-in tool.
  Mitigation: explicit OpenCode tools allowlist/deny config plus integration test proving only `remote_exec` is exposed.
- Risk: WebSocket-to-stream adapter deadlocks or leaks goroutines.
  Mitigation: package-level close/deadline tests and bounded copying; newest authenticated device connection wins.
- Risk: Embedded SSHD PTY behavior consumes the schedule.
  Mitigation: implement exec first and retain documented localhost OpenSSH fallback for the test environment.
- Risk: OpenCode events change across versions.
  Mitigation: tolerant decoder, preserve unknown event metadata, fixtures, startup version/capability probe.
- Risk: Direct provider thinking/tool-call wire contracts change.
  Mitigation: explicit HTTPS/no-redirect boundary, bounded SSE parser, typed safe errors, official-contract fixtures and deterministic multi-tool replay tests.
- Risk: OOM during parallel agent/build/test activity.
  Mitigation: implementation agents run only focused tests; main agent serializes full builds, race, browser, and remote tests.

## Review Questions

- Does each implementation preserve the fixed user/device/session binding across HTTP, Agent, SSH, and Tunnel layers?
- Are all local OpenCode tools actually disabled by configuration rather than prompt wording?
- Do disconnect/cancel paths close processes, SSH channels, yamux streams, SSE subscribers, and goroutines?
- Can the full deterministic chain pass with Fake Adapter when OpenCode free service is rate-limited?
- Can each real provider fail explicitly without falling back to Fake or obtaining Server-local execution authority?
