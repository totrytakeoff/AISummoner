---
task_id: task001
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 001 Plan: Go Foundation, SQLite, Auth, Pairing And Device API

## Status

Ready for implementation.

## Owner

Implementation Agent.

## Reviewer

Planner/Reviewer Agent.

## Context

The repository has authoritative MVP baselines but no code. This first task creates the stable Go/module/storage/HTTP foundation used by all later Tunnel, SSH, Agent, and Web tasks. It must not implement those later concerns.

## Goal

Produce a buildable Go Server skeleton with strict configuration, random IDs, SQLite migrations/store, single-admin authentication, one-time pairing, device ownership APIs, structured errors/logging, and focused tests.

## Relevant Files

- `go.mod`, `go.sum`
- `cmd/aisummoner-server/`
- `internal/config/`
- `internal/id/`
- `internal/store/`
- `internal/auth/`
- `internal/pairing/`
- `internal/device/`
- `internal/httpapi/`
- `migrations/`
- `.env.example`
- `Makefile`

## Required Behavior

- Server starts from environment configuration and creates the data directory/database safely.
- First start bootstraps exactly one admin using `AISUMMONER_ADMIN_PASSWORD`; later starts do not require it.
- Password hashes use the baseline Argon2id PHC format.
- Login issues a random HttpOnly/SameSite cookie; only its digest is stored; logout invalidates it.
- State-changing browser APIs validate same-origin requests.
- Pairing codes are unambiguous, normalized, HMAC-digested, one-time, ten-minute values.
- Pairing claim atomically binds an unowned device to the authenticated admin and consumes the code.
- Device list/detail/unpair enforce owner checks and return the baseline error envelope/request ID.
- Device online state is supplied through a narrow injected interface so task002 can add the live Connection Manager without changing API contracts.
- SQLite enables WAL, foreign keys, and busy timeout.

## Required Changes

- Create the initial migration for users, web_sessions, devices, pairing_codes, agent_sessions, agent_messages, tool_calls, and audit_events, including ADR-0002 Agent fields.
- Implement crypto-random prefixed IDs and deterministic Device ID helper.
- Implement typed store methods and transaction boundaries; do not expose raw SQL from HTTP handlers.
- Implement redaction-safe structured logging and request-ID middleware.
- Implement JSON limits and uniform errors.
- Add focused unit/store/HTTP tests for happy and failure paths.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/config ./internal/id ./internal/store ./internal/auth ./internal/pairing ./internal/device ./internal/httpapi
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server
```

## Documentation Requirements

- Write `docs/agent_context/tasks/task001/summary.md` with exact files, commands, pass/fail and deviations.
- Update `.env.example` and Make targets to match actual configuration.

## Out Of Scope

- WSS/yamux, Device challenge/heartbeat and live connections.
- Remote Client.
- SSH, PTY and Terminal WebSocket.
- WebUI.
- Agent runtime or OpenCode.
- Docker/Caddy deployment.

## Acceptance Criteria

The task is ready for review when:

- Both verification commands pass or environmental inability is recorded exactly.
- Auth, pairing one-time consumption, expiry, non-owner access, origin rejection and error envelope have tests.
- No secret value is logged or committed.
- Later tasks can construct the Server and inject online-state behavior without rewriting the foundation.
