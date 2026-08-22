---
task_id: task003
type: plan
status: ready_for_revision
from: planner
to: coder
revision: 4
requires_review: true
---

# Task 003 Plan: Embedded SSHD, Remote Exec And PTY

## Status

Ready for implementation after task002 review approval.

## Owner

Implementation Agent.

## Reviewer

Independent Reviewer Agent.

## Context

Task002 provides authenticated Device Tunnels and a server-opened `ssh` yamux stream. This task implements the standard SSH execution plane used later by both Browser Terminal and Agent tools.

## Goal

Implement a strict Embedded SSH Server on Remote and a Server-side SSH dialer that support non-PTY exec, interactive PTY shell, resize, exit status, cancellation and resource cleanup without listening on a TCP port.

## Revision 3 Reopened Boundary

Task008's final merged `go test -race ./internal/...` found a real retained
Task003 race in `TestTunnelSSHEndToEnd`: an SSH `window-change` request calls
`pty.Setsize`, which reads `os.File.Fd`, while context/transport termination can
concurrently close the same PTY file. Revision 3 is explicitly limited to
`internal/sshserver/server.go`, its focused tests, and this task summary.

- Linearize every PTY resize operation with every production PTY close path.
  Do not merely hide the race with a test delay, recover, or ignored error.
- A resize after terminal close/finalization fails closed. Normal resize before
  close retains the approved dimensions and response behavior.
- Do not hold the process identity/lifecycle mutex while waiting on PTY I/O or
  pumps, and document the resulting lock order.
- Add a deterministic barrier regression that holds an in-flight resize at the
  file-operation boundary, starts termination/final close, proves close cannot
  cross that boundary, then releases and joins both paths. Add a late-resize
  regression and rerun the exact full Tunnel/SSH composition under `-race`.
- Preserve all revision-2 pidfd, descendant cleanup, pump join and public SSH
  behavior. No Terminal HTTP, Tunnel protocol, Agent, Web or deployment change
  is authorized by this revision.

Revision 4 is a test-only response to the revision-3 review. Replace the
pre-call `terminationStarted` signal with a package-private, production-path
close-contender admission hook (or an equivalent deterministic injected gate)
that fires only after `closeTerminal` has actually reached the terminal
lifecycle boundary and before it acquires `terminalMu`. The regression must
wait for that point while resize holds the mutex, prove close/termination stay
blocked, then release and join. Removing the close-side mutex must make the
test fail deterministically. No production lifecycle behavior change is
authorized unless the hook cannot be added without one; report that blocker
instead of broadening scope.

## Relevant Files

- `internal/sshserver/`
- `internal/sshclient/`
- `internal/tunnel/` only to attach the existing SSH StreamHandler or expose narrow stream metadata
- `internal/identity/` only for a narrow method that returns an SSH signer backed by the existing private Device Identity without exposing private bytes
- `cmd/aisummoner-client/main.go` only for SSH handler wiring
- `go.mod`, `go.sum` only for `creack/pty`
- `Makefile` only for relevant test target
- `docs/agent_context/tasks/task003/summary.md`

## Required Behavior

- Remote uses Device Identity as SSH Host Key.
- Remote accepts only username `aisummoner` and the connection-scoped SSH public key sent by authenticated control protocol.
- Server verifies Remote SSH Host Key exactly against Device Registry public key; never use `InsecureIgnoreHostKey`.
- Each SSH transport lives on one Server-opened `kind:ssh` yamux stream.
- Supports `session` channels only; rejects forwarding, agent, X11 and unknown channels.
- Supports `exec` through the Remote user's `$SHELL -lc <command>` with stdout, stderr and exit-status.
- Supports `env` only for bounded `AISUMMONER_CWD`; rejects all other environment requests.
- cwd must be an existing absolute directory before assigning `exec.Cmd.Dir`.
- Supports `pty-req`, `shell`, `window-change`, stdin/stdout and process exit.
- PTY initial and resize dimensions are bounded to baseline limits.
- Context cancel, channel close and Tunnel close terminate/reap child processes and close resources.
- Commands and terminal input are not logged.

## Required Public Boundaries

- Add a safe Device Identity method such as `SSHSigner() (ssh.Signer, error)`; do not export/serialize the Ed25519 private-key bytes.
- Add one atomic Tunnel operation that opens an SSH stream and returns the signer belonging to the exact same live Connection. Do not independently call `Manager.Get`, `Manager.OpenStream`, and `Connection.SSHClientSigner`, because newest-wins could mix stream and credentials from different connection instances.
- `sshclient.Dialer` receives the Tunnel stream opener and a Device public-key lookup. Its handshake uses user `aisummoner`, the exact connection signer, and a host-key callback that byte-compares the SSH Ed25519 key with the registered raw Device key. Hostname/ID unpredictability is not authentication.
- Expose a bounded non-PTY result API with separate stdout/stderr, exit code and transport error.
- Expose a PTY handle with `Input`, `Output`, `Resize(cols, rows)`, `Wait`, and idempotent `Close` (exact names may differ) so task005 never imports raw SSH internals.
- Every opened SSH stream/client/session has one cancellation owner and an idempotent close path.

## Required Changes

- Implement SSH Server config/session request parser and process runner.
- Implement Server dial helper taking task002 Connection, registered Device public key and context.
- Provide narrow Exec and PTY APIs for task005/task006.
- Add in-memory/task002 manager integration tests for strict keys, exec output/stderr/status, bad user/key/host, cwd, PTY resize and cancellation.

## SSH Server State Machine

- Use `ssh.NewServerConn` directly over the accepted yamux stream; never bind a TCP listener.
- Reject global requests and every channel type except `session`. Reply false where SSH semantics request a reply.
- Per `session` channel, allow at most one launch (`exec` or `shell`). Bound request payloads and command length to the protocol/tool limits.
- `env` accepts exactly one bounded `AISUMMONER_CWD` absolute path. Validate with `os.Stat` immediately before launch and require an existing directory. Reject every other environment variable.
- `pty-req` validates terminal-name length and cols 1-500 / rows 1-300. It may occur only before `shell`; `window-change` is valid only for a running PTY and applies the same bounds.
- `exec` must not allocate a PTY. Execute `exec.CommandContext` semantics as the Remote process user using the selected `$SHELL` (fallback `/bin/sh`) and arguments `-lc`, `<command>`; never build `cd ... &&` or a Server-local command.
- `shell` requires a successful PTY request, starts the user's interactive shell on a Linux PTY, bridges stdin/output and applies initial/updated window size.
- Support the minimal `signal` requests needed for INT/TERM/KILL and fail unknown signals closed.
- Always send an SSH `exit-status`, including non-zero exits, before closing a launched channel when transport permits. Preserve stdout and stderr separately for non-PTY exec.
- On context/SSH/Tunnel/channel close, terminate the whole child process group, reap it, close the PTY and channel, and return promptly. Avoid `CommandContext` paths that kill only the parent while leaving descendants.
- Commands, cwd, environment values, stdin, stdout and stderr must never be written to logs.

## SSH Client Semantics

- Clear any stream setup deadline before starting SSH, but keep context cancellation wired to stream/client closure.
- Do not use `InsecureIgnoreHostKey`, known-host fallback, TOFU, or key-type-only validation.
- Treat non-zero remote exit as an `ExecResult` with its exit code, not as loss of the Tunnel. Authentication/transport/protocol failures remain typed errors.
- Apply a bounded capture (at least the Agent's 256 KiB contract, preferably caller-supplied with a hard maximum) so an untrusted Remote cannot exhaust Server memory. Expose a truncation flag while draining safely or closing on limit.
- `OpenPTY` requests `xterm-256color`, returns only after `shell` succeeds, and maps browser `(cols, rows)` to SSH `WindowChange(rows, cols)` correctly.
- A canceled Exec/PTY closes only its SSH stream/session, not unrelated Device Tunnel streams.

## Verification

```bash
GOMAXPROCS=2 go test -p 2 ./internal/sshserver ./internal/sshclient ./internal/tunnel
GOMAXPROCS=2 go build -p 2 ./cmd/aisummoner-server ./cmd/aisummoner-client
```

## Documentation Requirements

- Write `docs/agent_context/tasks/task003/summary.md` with exact verification.

## Out Of Scope

- Browser WebSocket/xterm gateway.
- SFTP/SCP/port forwarding.
- Terminal reconnect.
- Agent/OpenCode.
- systemd/deployment.

## Acceptance Criteria

The task is ready for review when:

- Strict Server and Remote key checks pass and negative key/user/host tests fail closed.
- Non-PTY exec captures separate stdout/stderr and correct exit status.
- cwd validation and timeout/cancel are tested.
- Interactive shell and window resize work in an automated PTY integration test.
- Process/stream closure has no obvious goroutine/child leak.
- Newest-wins cannot mix an old SSH signer with a new Connection stream.
- Oversized command/env/PTY requests and forbidden channel/global/env/signal requests fail closed.
- Required tests and both builds pass on ASD-Host or exact failure is recorded.
