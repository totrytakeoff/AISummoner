---
type: project_context
status: active
updated_by: task014_implementation
---

# Project Context

## Purpose

AISummoner is a browser-controlled, server-side Agent and SSH remote execution platform. A Linux Remote Client initiates an outbound WSS connection to a single Server. The browser pairs devices, opens a terminal, and drives a provider-neutral Agent that can execute only against the bound Remote.

## Tech Stack

- Language/runtime: Go for Server/Remote, TypeScript for WebUI and the OpenCode custom tool.
- Frameworks: Go `net/http`, React/Vite, xterm.js.
- Transport: HTTPS/WSS, coder/websocket, yamux, `x/crypto/ssh`, creack/pty.
- Build system: Go modules, npm lockfile, Docker multi-stage build.
- Test system: `go test`, Vitest, Playwright with one worker, shell-driven three-host smoke tests.
- Storage/services: SQLite WAL via modernc.org/sqlite; direct HTTPS DeepSeek or a loopback OpenCode headless sidecar; Caddy deployment example.

## Repository Map

- `cmd/aisummoner-server/`: Server executable.
- `cmd/aisummoner-client/`: Remote Client executable.
- `internal/`: Go packages owned by narrow tasks.
- `web/`: React WebUI.
- `migrations/`: embedded SQLite migrations.
- `deploy/`: Docker/Caddy and remote deployment assets.
- `docs/baseline/`: authoritative behavior and acceptance baseline.
- `docs/decisions/`: accepted architecture decisions.
- `docs/agent_context/`: durable multi-agent workflow and task handoffs.

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

- Tasks001-010 are independently approved; Task011 produced the bounded ASD deployment and Linux AppImage; Tasks012-013 implemented the provider-neutral ordered Agent timeline and resumable conversation behavior; Task014 deployed direct DeepSeek with Web key entry and removed the cumulative Turn tool-count wall. The user confirmed the complete Terminal and Agent vertical slice on the real Remote.
- ADR-0001 fixes Go/React/WSS/yamux/Embedded SSHD/SQLite.
- ADR-0002 replaced the original OpenAI Agent Loop with an OpenCode loopback sidecar plus a Fake Adapter for MVP-0.
- ADR-0003 defines the provider-neutral Agent/Web seams and permits direct adapters such as DeepSeek without adding a second execution or session authority.
- Server is single-node and authoritative for user/device/session ownership.
- Online Tunnel connections live only in memory.
- OpenCode custom `remote_exec` calls a Go loopback bridge; the direct DeepSeek Adapter invokes the same Go `RemoteExecInvoker`. In both cases the target device is derived from the owned AISummoner Session, never model input.
- Deterministic tests must not depend on OpenCode free-tier availability.

## Constraints

- MVP-0 means the 12 acceptance checks in `docs/baseline/00-product-scope.md`, not file transfer, port forwarding, desktop, or multi-user features.
- Main agent owns architecture, integration, heavy builds, remote deployment, and final acceptance.
- Implementation agents own narrow file sets and must write task summaries.
- Preserve TLS, Device challenge, pairing single-use, ownership checks, SSH key verification, Agent approval, deadlines, and output limits.
- Do not store or print passwords, SSH keys, session tokens, OpenCode credentials, or terminal keystrokes.
- Local heavy work starts only with >=8 GiB MemAvailable and >=4 GiB free swap; parallel build/test jobs <=2.
- ASD-Host is the target Server/OpenCode machine; lzr-host is Remote Client only.

## Known Risks

- Direct DeepSeek is the proven interactive Provider. Its key can be entered through the authenticated Web form and remains only in Server process memory; the previously exposed diagnostic credential remains ineligible.
- Local machine has no Go toolchain; use controlled Docker build or ASD-Host until installed.
- lzr-host has only about 1.8 GiB MemAvailable and no swap; do not build Node/OpenCode there.
- WSS byte-stream/yamux close semantics and Embedded SSHD PTY lifecycle are high-risk.
- OpenCode 1.18.11 event schema and direct DeepSeek thinking/tool-call streaming are covered by bounded provider fixtures; live provider availability and billing remain external dependencies.
