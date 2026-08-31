---
task_id: task024
type: summary
status: ready_for_review
from: coder
to: human_reviewer
revision: 0
review_required: true
---

# Task 024 Summary: Common Remote Core Platform Seams

## Outcome

The common-platform extraction is complete. Production Linux behavior now sits
behind four explicit boundaries while the Remote controller, Tunnel state,
Identity semantics, IPC protocol dispatch and SSH wire/session protocol remain
shared. The Tunnel hello contract recognizes exactly `linux|windows` without a
protocol-version change.

Windows is still not a supported production Remote. No no-op Windows backend
was added: a production `GOOS=windows` client build stops at the intentionally
missing runtime, Identity and SSH execution constructors, with named-pipe IPC
also awaiting its Task025 implementation. Proposed ADR-0007 remains Proposed.

## Implemented Boundaries

### Process-level platform policy

- Added `internal/clientplatform.Runtime` for the target name, per-user data
  root, data-directory validation, privilege policy and shutdown notification.
- The Linux build-tagged implementation preserves
  `$HOME/.local/share/aisummoner`, the two-flag root development exception and
  SIGINT/SIGTERM shutdown.
- `cmd/aisummoner-client` no longer imports Unix signal/root/path primitives,
  and `remoteclient.New` derives hello metadata from the selected runtime
  instead of hard-coding Linux.

### Identity storage

- Common code continues to own Ed25519 generation, Device ID derivation,
  metadata consistency, transcript signing and the exact SSH host signer.
- The new storage contract owns private-key loading/creation and metadata
  loading/writing.
- `storage_linux.go` retains PKCS#8 PEM, mode-0700 directory, mode-0600 key and
  metadata, exclusive key creation and fail-closed partial-state behavior.
- In-memory contract tests prove stable reload, metadata mismatch rejection and
  refusal to replace an orphaned identity.

### Private local IPC

- New `localTransport` and `authenticatedListener` contracts own endpoint
  syntax, default endpoint, dial/listen and exact-peer authentication.
- JSON v1 framing, strict duplicate/unknown-field rejection, 64 KiB limit,
  deadlines, eight-handler admission and controller dispatch remain common.
- The Linux transport retains the bounded absolute Unix endpoint, containment
  in the private data directory, mode/owner/inode checks, safe stale cleanup
  and `SO_PEERCRED` same-UID verification before request bytes are read.
- `ServerOptions` now uses the platform-neutral `Endpoint` name.

### SSH execution

- `sshserver/server.go` now owns only SSH handshake/key verification,
  channel/request parsing, portable INT/TERM/KILL requests, cwd delegation,
  payload/window limits, exit-status framing and joined Session completion.
- `executionBackend` and `sessionProcess` define path syntax, cwd validation,
  exec/shell start, resize, signal, termination, wait and final join.
- `process_linux.go` contains `/bin/sh`, `creack/pty`, process groups/sessions,
  pidfd anchoring, `/proc` identity checks, repeat descendant scans and the
  existing resize/finalization lock ordering.
- Fake-backend tests prove common SSH code accepts backend-defined Windows path
  syntax and maps only the supported SSH signals.

### Tunnel metadata

- `internal/protocol` is the single strict platform enum source.
- Client construction and Server hello validation accept Linux and Windows and
  reject empty, unknown, case-mutated and NUL-bearing values.
- The production Linux Remote always reports the build target selected by
  `clientplatform`; Browser input cannot choose it.

## Verification

### Passed

- Focused Linux Go packages, including the real SSH process chain:
  `clientplatform`, `identity`, `clientipc`, `protocol`, `remoteclient`,
  `sshserver`, `sshclient`, `tunnel` and `cmd/aisummoner-client`.
- Focused race gate over the same internal packages: PASS.
- `go vet -p=1 ./...`: PASS.
- Windows Task023 probe and CLI, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`:
  build and `go vet`: PASS.
- Linux Qt 6.11.2 RelWithDebInfo build and offscreen CTest: PASS, 1/1 in
  2.30 seconds.
- Containerized Ubuntu 22.04-class Qt Release/CTest and AppDir contract: PASS;
  maximum bundled ABI is `GLIBC_2.34`.
- Linux GUI+daemon engineering AppImage: 34,773,496 bytes, SHA-256
  `09f127726ea01e0f0979e08f7fe3dd8a25472266a3e632ebaf1314ee00ad04cd`.
  The checksum was verified from its output directory.
- AppImage packaging now explicitly passes `-processors 2` to mksquashfs;
  the final log confirms exactly two processors rather than the previous host
  default of sixteen.
- Web regression: 16 files / 82 tests PASS; TypeScript/Vite production build
  PASS. No Web source changed.

### Repository-wide known red gate

Both `go test -p=1 -count=1 ./...` and `go test -p=1 -race ./internal/...`
execute every package but remain red only at the pre-existing DSH timing test
`TestStartHostTimeoutCleansExactChild`: its 40 ms startup context can expire
before the fixture writes `child.pid`. Task023 already recorded the same
failure, Task024 changes no DSH file, and every Task024 package passes its
focused race gate. This is not represented as a green full-suite result.

An initial SSH lifecycle run omitted Docker `--init`; killed grandchildren then
remained as zombies under the container's non-reaping PID 1. The exact failure
was reproduced at the pre-Task024 commit. Re-running the current code with
`--init` made all exec, PTY and Tunnel descendant-reaping tests pass. All final
lifecycle gates therefore use an init process.

One AppImage retry failed while resolving the pinned Dockerfile frontend from
Docker Hub; the immediate retry used the same pins and completed. No dependency
pin or security check was relaxed.

## Intentional Windows Compile Boundary

The production Windows build now reports missing platform constructors rather
than Unix struct/API errors:

```text
clientplatform: currentRuntime
identity:       newStorage
sshserver:      currentExecutionBackend
```

`clientipc.currentTransport` is the fourth backend and is reached once the
upstream Core dependencies exist. Task025 should implement Windows Runtime,
LocalAppData, token policy, DPAPI storage and named-pipe transport using the
proven `internal/windowsprobe` contracts. Task026 should add the real
PowerShell/Job execution backend while keeping unsupported operations fail
closed; Task027 then replaces its unsupported interactive-shell path with
ConPTY. No interface redesign is required by the current inventory.

## Remaining Gates

- Independent code review is not fabricated; this revision awaits review.
- Task023 ordinary-user Windows 11/10, wrong-logon and clean-VM evidence remains
  open and still blocks accepting ADR-0007.
- No ASD service or external deployment was touched.
