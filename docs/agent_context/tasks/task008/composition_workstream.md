---
task_id: task008-composition
type: workstream_plan
status: ready_for_implementation
from: planner
to: composition_coder
revision: 0
requires_review: true
---

# Task008 Composition Workstream

## Objective

Produce one runnable Server, one runnable Remote Client, embedded production
Web assets and reproducible deployment files by composing approved package
boundaries. This workstream consumes, but does not edit, lifecycle-owned files.

## Owned Files

- `cmd/aisummoner-server/`, `cmd/aisummoner-client/`
- `internal/config/`
- `internal/app/` and `internal/staticweb/` (new if needed)
- Web production embed/build glue only; no UI redesign
- `deploy/`
- root `Makefile`, `.gitignore`, `.env.example`, `README.md`
- `docs/agent_context/tasks/task008/summary.md`

Do not edit `internal/devicegate/`, `internal/device/`, `internal/tunnel/`,
`internal/store/`, `internal/agent/`, Terminal, SSH, OpenCode or protocol state
machines. Report an incompatible boundary instead of patching it here.

## Required Composition

1. Validate private data/workspace directories and exact adapter selection.
   Fake mode needs no OpenCode credentials or Bridge listener. OpenCode mode
   requires a literal loopback provider URL, loopback Bridge listener, Basic
   Auth credentials, model and at least 32-byte Bridge secret. Secrets never
   appear in errors/logs.
2. Construct SQLite/Auth/Pairing, one shared Device gate, Tunnel Manager and
   Gateway, strict SSH Dialer, Terminal closure adapter, Agent executor closure,
   Fake or OpenCode Adapter, Agent API, Browser API and Device Service.
   SSH exec capture is `agent.MaxToolOutputBytes + 1` before mapping the product
   truncation flag.
3. Run OpenCode Bridge on a separate loopback listener only. It must never be
   mounted on the public mux. In OpenCode mode, bind the Bridge before creating
   the Adapter and give the tool its derived callback URL/secret only through
   process environment.
4. Top-level dispatch is by exact path shape and forwards the original
   `ResponseWriter`: Tunnel, Terminal, Agent REST/SSE, health, Browser JSON,
   static assets/SPA. It must preserve Hijacker and ResponseController/flush.
   Wrong-method API routes never fall through to HTML.
5. Serve tracked placeholder embed content in a clean Go checkout. Production
   Docker build runs `npm ci`, tests/build as appropriate, copies `web/dist`
   into the embed source, then compiles Go. Hashed assets are immutable-cache;
   index/SPA no-cache; missing assets and traversal are 404.
6. Public health gates new work and probes SQLite with a short deadline; it
   does not claim OpenCode/model health. Shutdown first quiesces admission,
   then joins Agent/OpenCode, Terminal/SSH and Tunnel lifecycles, stops Bridge
   and public HTTP, and closes SQLite last under one bounded deadline.
7. Compose deployment with Caddy as the only published HTTP(S) endpoint.
   OpenCode shares the Server network namespace so both loopback-only ports are
   the same namespace; neither OpenCode nor Bridge is published. Use shared
   absolute workspace volume/path and compatible non-root UID/GID.

## Required Tests And Builds

- Config matrix and redaction.
- One real in-process composition proving Browser JSON, Device Tunnel upgrade,
  Terminal upgrade, Agent SSE immediate flush, static cache/fallback and API
  miss separation through the top-level dispatcher.
- Fake Adapter startup without OpenCode settings; OpenCode mode loopback
  failure/health behavior and Bridge non-public reachability.
- Remote Client SSH stream composition and bounded shutdown.
- Shutdown with open SSE/WebSocket/Tunnel fixtures joins without hanging.
- Static clean-checkout placeholder and production asset tests.
- Sequential limited Go tests/builds, Web `npm ci`/test/build,
  `docker compose config`, and Docker image build when resources allow. Record
  skipped or failed commands exactly.

## Handoff

Consume the lifecycle workstream's `ready_for_integration` summary before final
composition tests. Write the single parent `task008/summary.md` covering both
workstreams and set it to `ready_for_review`; do not self-review.
