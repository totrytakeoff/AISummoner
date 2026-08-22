---
type: roadmap
status: active
updated_by: task014_implementation
---

# Roadmap

## Phase 1: Foundation

- [x] task001: Go module, configuration, IDs, SQLite, auth, pairing, device HTTP API.
- [x] task002: Device identity, WSS/yamux control tunnel, heartbeat, reconnect, connection manager (approved revision 2).
- [x] task003: Embedded SSHD, strict SSH client verification, exec and PTY lifecycle (approved revision 2).

## Phase 2: Product Surfaces

- [x] task004: React WebUI for login, pairing, devices, terminal and Agent activity (approved revision 1).
- [x] task005: Terminal WebSocket gateway and xterm integration (approved revision 1).
- [x] task006: Agent domain, approval state machine, Fake Adapter, SSE API (approved revision 2).
- [x] task007: OpenCode sidecar adapter, custom `remote_exec` tool and bridge (approved revision 0).

## Phase 3: Integration And Delivery

- [x] task008: Full lifecycle, Server composition, static embed and deployment assets (approved revision 0).
- [x] task009: Integration review, trusted-proxy/resource hardening and merged verification (approved revision 2).
- [x] task010: Local build/test, ASD-Host/lzr-host deployment, three-host E2E and acceptance record (approved revision 2 on authorized TCP 10001).
- [x] task011: Bounded ASD test deployment and Linux x86_64 Client AppImage handoff.
- [x] task012: Provider-neutral ordered Agent timeline and DSH-inspired Web interaction (implemented; review pending).
- [x] task013: Reliable OpenCode Turn completion and resumable Agent conversation (implemented; review pending).
- [x] task014: Direct DeepSeek streaming Adapter, Web memory-only key entry,
  no-wizard native Agent interaction and no cumulative Turn tool-count wall;
  deployed and human-confirmed across the real Remote chain.

## Later

- [ ] Agent UX: planning efficiency, context compaction, cancel/regenerate,
  richer Markdown/code output and additional provider presentation adapters.
- [ ] Alpha hardening: runtime containers, session recovery, key rotation, installers and update path.
- [ ] File/port features, multi-user authorization and desktop assistance remain out of MVP-0.
