---
type: project_context
status: active
updated_by: coder
---

# Project Context

## Purpose

AISummoner is a browser-controlled, server-side Agent and SSH remote execution platform. A Linux Remote Client initiates an outbound WSS connection to a single Server. The browser pairs devices, opens a terminal, and drives a provider-neutral Agent that can execute only against the bound Remote. MVP-0 proved this vertical slice. Alpha now has a first usable DSH-first Controller and Qt Remote Client; the next work is richer Runtime compatibility and additional Remote platforms.

## Tech Stack

- Language/runtime: Go for Server/Remote, TypeScript for WebUI/Runtime tools,
  and C++20 for the Qt Remote GUI.
- Frameworks: Go `net/http`, React/Vite, xterm.js, Qt 6 Widgets.
- Transport: HTTPS/WSS, coder/websocket, yamux, `x/crypto/ssh`, creack/pty.
- Build system: Go modules, npm lockfile, Docker multi-stage build.
- Test system: `go test`, Vitest, Playwright with one worker, shell-driven three-host smoke tests.
- Storage/services: SQLite WAL via modernc.org/sqlite; private loopback DSH,
  direct HTTPS DeepSeek, Fake or loopback OpenCode; Caddy deployment example.

## Repository Map

- `cmd/aisummoner-server/`: Server executable.
- `cmd/aisummoner-client/`: Remote Client executable.
- `internal/`: Go packages owned by narrow tasks.
- `web/`: React WebUI.
- `desktop/remote-client/`: Qt Remote GUI.
- `migrations/`: embedded SQLite migrations.
- `deploy/`: Docker/Caddy and remote deployment assets.
- `docs/baseline/`: authoritative behavior and acceptance baseline.
- `docs/decisions/`: accepted architecture decisions.
- `docs/agent_context/`: durable multi-agent workflow and task handoffs.
- `deepseek-harness` is a separate local reference checkout, not a runtime or
  source dependency of this repository.

## Build And Test Commands

- Go build: `GOMAXPROCS=2 go build -p 2 ./...`
- Go test: `GOMAXPROCS=2 go test -p 2 ./...`
- Go race: run separately with `GOMAXPROCS=2 go test -p 2 -race ./internal/...`
- Web install: `npm --prefix web ci`
- Web build: `NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build`
- Web test: `npm --prefix web test`
- E2E: `npm --prefix web run e2e -- --workers=1`
- Compose check: `docker compose -f deploy/compose.yaml config`

## Current Architecture Facts

- Tasks001-014 proved and deployed the MVP vertical slice. Tasks015-016 added
  the Remote daemon/private IPC and Qt AppImage. Tasks017-021 produced the
  DSH-first Controller workspace, real DSH Runtime chain, Session permission/
  recovery/lifecycle behavior and the first human-accepted Controller milestone.
  Task022 adds DSH-native redacted Provider management plus current-Session
  provider/model/reasoning selection behind optional common Adapter capabilities.
- ADR-0001 fixes Go/React/WSS/yamux/Embedded SSHD/SQLite.
- ADR-0002 replaced the original OpenAI Agent Loop with an OpenCode loopback sidecar plus a Fake Adapter for MVP-0.
- ADR-0003 defines the provider-neutral Agent/Web seams and permits direct adapters such as DeepSeek without adding a second execution or session authority.
- Baseline 04 and ADR-0004 define the Alpha direction: Device-scoped Control
  Workspace, DSH-inspired Agent interaction, capability-driven Runtime
  adapters, and a Remote daemon/Desktop UI split.
- Task017's Workspace foundation is independently approved. ADR-0005 and
  Task018 replace its provisional presentation with the pinned DSH-first light
  shell, retire Device Manage, and put Agent/Device configuration in Workspace
  Settings while keeping Experience and Runtime adapters separate.
- ADR-0006 separates Runtime identity, Host-level Provider configuration and
  current-Session model selection. DSH remains the fact source; future
  file-backed adapters must use managed-document atomic/rollback semantics.
- Server is single-node and authoritative for user/device/session ownership.
- Online Tunnel connections live only in memory.
- DSH and OpenCode call a Go loopback Capability Bridge; direct DeepSeek uses
  the same Go `RemoteExecInvoker`. In every case the target Device is derived
  from the owned AISummoner Session, never model input.
- Deterministic tests must not depend on OpenCode free-tier availability.
- Legacy Controller routes remain safe migration redirects. The DSH-first
  Workspace and real DSH execution chain are now the primary Alpha experience;
  event v2, richer native actions and the other Runtime adapters remain work.
- Task015/016 implemented the Qt 6 Widgets Remote Desktop Client and a
  zero-configuration GUI+daemon AppImage. Linux is the only supported Remote
  platform today. Proposed ADR-0007 and the Windows port design define a
  common-Core/platform-backend approach: per-user background lifecycle,
  authenticated named-pipe IPC, DPAPI identity, PowerShell/ConPTY/Job Objects,
  native Qt packaging and a target-aware Agent execution profile. Task023's
  Windows Server 2022 CI proved the reusable native API/toolchain contracts;
  Task024 extracted production Runtime policy, Identity storage, IPC transport
  and SSH execution seams; Task025 connected real Windows LocalAppData/token
  policy, DPAPI/ACL Identity and authenticated named-pipe IPC. Task026 now adds
  production inbox-PowerShell non-PTY exec, suspended Job assignment and joined
  process-tree cleanup. Task027 adds production PowerShell/ConPTY Terminal with
  VT/UTF-8, cwd, resize, Ctrl-C and joined cleanup, proven through the same
  native TLS/WSS/yamux/strict SSH chain. Ordinary-user Windows 11/10,
  wrong-logon, clean-VM GUI launch, real Agent and public production E2E remain,
  so Windows is not yet supported and ADR-0007 remains Proposed.

## Constraints

- MVP-0 is frozen historical evidence. Alpha implementation is authorized only
  one narrow task at a time by `docs/baseline/04-alpha-product-direction.md`
  and the active task plan.
- Main agent owns architecture, integration, heavy builds, remote deployment, and final acceptance.
- Implementation agents own narrow file sets and must write task summaries.
- Preserve TLS, Device challenge, pairing single-use, ownership checks, SSH key verification, Agent approval, deadlines, and output limits.
- DSH/OpenCode/Codex/Claude may not select a Device or use Server-local
  shell/filesystem; all tools pass through the Remote Capability boundary.
- Remote GUI may talk only to the local daemon over authenticated private IPC;
  it must not expose a new remote-control listener or read Device private keys.
- Do not store or print passwords, SSH keys, session tokens, OpenCode credentials, or terminal keystrokes.
- Local heavy work starts only with >=8 GiB MemAvailable and >=4 GiB free swap; parallel build/test jobs <=2.
- ASD-Host is the target Server/OpenCode machine; lzr-host is Remote Client only.

## Known Risks

- DSH is the primary interactive Runtime. Its DeepSeek key is written only to
  the private mode-0600 DSH credential store and is never returned to Browser,
  SQLite, audit or logs. Direct DeepSeek remains a legacy compatibility path.
- Local machine has no Go toolchain; use controlled Docker build or ASD-Host until installed.
- lzr-host has only about 1.8 GiB MemAvailable and no swap; do not build Node/OpenCode there.
- WSS byte-stream/yamux close semantics and Embedded SSHD PTY lifecycle are high-risk.
- OpenCode 1.18.11 event schema and direct DeepSeek thinking/tool-call streaming are covered by bounded provider fixtures; live provider availability and billing remain external dependencies.
- DSH is pre-1.0 and all external Runtime contracts can change; pin exact
  versions and keep captured fixtures/generated schema.
- A richer coding Agent needs structured Remote file/patch capabilities beyond
  `remote_exec`; adding them without leaking Server-local authority is a major
  Alpha design risk.
- Qt 6 Widgets is the settled Remote GUI toolkit. Its Ubuntu-compatible
  AppImage is built from a pinned Ubuntu 22.04/Qt 6.2 class environment; future
  Remote GUI work must preserve the private daemon IPC split.
