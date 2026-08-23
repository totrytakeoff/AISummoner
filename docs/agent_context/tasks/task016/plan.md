---
task_id: task016
type: plan
status: ready_for_implementation
from: planner
to: coder
revision: 1
requires_review: true
---

# Task 016 Plan: Qt Remote Desktop Client And AppImage

## Goal

Build the user-selected Qt 6 Widgets GUI for the controlled Linux device. It
must consume Task015 private IPC rather than own Device keys, Tunnel, SSH or
remote commands, and ship with the Go daemon as one non-root AppImage.

## Inputs

- `docs/design/remote-client-qt.md`
- `docs/design/remote-client-ipc-v1.md`
- Task015 frozen Core/IPC implementation and verification
- Claude Code's no-write Apple-like UI review; AISummoner security boundaries
  override any aesthetic suggestion

## Toolkit And Layout

- C++20, CMake, Qt 6 Core/Gui/Widgets/Network/Test.
- Native `QMainWindow`; no QML, WebEngine, custom window chrome, local HTTP
  server, embedded shell or privileged helper.
- Sources under `desktop/remote-client/`, executable
  `aisummoner-client-ui`.
- Default size 940×640, minimum 780×560. Left navigation selects Status,
  Events and Settings; content pages scroll instead of overlapping.

## Required Behavior

### Async private IPC

- `DaemonClient` creates one `QLocalSocket` per request, frames newline JSON v1,
  caps both directions at 64 KiB and uses a bounded timer. UI thread never calls
  `waitFor*`, sleeps or blocks on processes.
- Validate version, request id, success/error exclusivity, exact known fields,
  types, phases and event levels. Duplicate JSON object keys are rejected before
  `QJsonDocument` consumption.
- Poll status at most once per second and events incrementally by
  `next_sequence`; never overlap calls of the same class. Reconnect does a full
  snapshot then resumes the event cursor.
- Map fixed daemon error codes to fixed Chinese UI text. Do not display/log raw
  frames or secret-bearing status payloads.

### Status page

- Explicit icon+text status for starting/connecting/online/retrying/paused/
  stopped/error and IPC unavailable.
- Device name/ID with copy action; Server origin read-only; active generic
  control-session total only (never falsely Terminal versus Agent).
- Pairing card appears only for an offer, uses a monospace code, countdown,
  copy and refresh. Expired state contains no code. Refresh requires a dialog
  that explicitly says active sessions close.
- Pause/resume are async, idempotency-protected and show progress/fixed errors.
  GUI close sends no daemon action.
- Recent sanitized events preview; Events page retains at most 200 rows and
  filters All/Connection/Control locally.

### Settings and daemon recovery

- Persist only non-secret Server HTTPS origin, Device display name and theme in
  `QSettings`; never persist pairing codes, response frames, commands or keys.
- When IPC is unavailable, Start Service launches only the verified sibling
  `aisummoner-client` binary with `daemon`, the fixed private data directory,
  configured HTTPS origin and optional bounded name. No shell, root, `--dev`,
  `--allow-root-dev` or caller-controlled executable path.
- Settings changed while daemon is active are clearly marked “next daemon
  start”; v1 must not pretend to hot-reconfigure Core.
- GUI exit leaves daemon running. Start is duplicate-click guarded and
  availability polling proves convergence.

### Visual/accessibility

- Central palette/QSS implements the accepted calm neutral/blue light and dark
  tokens, 16px cards, 10px controls, visible focus ring and native title bar.
- Theme values: follow system/light/dark. Status is never color-only.
- All controls have accessible names/descriptions, deterministic Tab order,
  keyboard activation and dialog cancel. Copy actions give bounded visual
  feedback without echoing the copied value.
- No screenshots, traces, analytics or secret-bearing debug output in tests.

## AppImage

- Bundle `aisummoner-client-ui`, the verified Go `aisummoner-client`, required
  Qt runtime libraries/plugins, desktop entry and icon.
- `AppRun` launches the GUI by default and may forward an explicit `--cli`
  compatibility mode to the Go binary without shell evaluation.
- Build in a pinned Ubuntu-compatible environment or otherwise prove the
  resulting glibc/Qt closure on non-root Ubuntu. Do not package the host Arch
  Qt runtime as if it were portable.
- Output includes SHA-256 and an extracted AppDir contract check. Packaging
  failure due missing network/tooling must be recorded, not reported as pass.

## Required Tests

- Strict JSON/framing: success, malformed, oversized, duplicate, unknown,
  mismatched id/version, invalid phase/event and fixed error mapping.
- Real `QLocalServer` fake: status/event polling, disconnect/reconnect,
  pause/resume/refresh request shapes, timeout and no overlapping poll.
- Widget/offscreen: 780×560 geometry, three navigation pages, every phase,
  pairing countdown/expiry/copy/confirm, action pending/error recovery, event
  filter and theme switch.
- Daemon launcher: sibling-only executable, exact args/no dev/root, invalid
  Server refusal, duplicate start guard and GUI close emits no pause.
- Secret sentinels absent from application logs/test output/settings; no pairing
  code outside the in-memory Status model and clipboard user action.
- Build/test with `QT_QPA_PLATFORM=offscreen`; run `ctest` and a real local
  daemon/GUI smoke without changing ASD services.

## Verification

```bash
cmake -S desktop/remote-client -B build/remote-client \
  -DCMAKE_BUILD_TYPE=RelWithDebInfo -DBUILD_TESTING=ON
cmake --build build/remote-client --parallel 2
QT_QPA_PLATFORM=offscreen ctest --test-dir build/remote-client \
  --output-on-failure
```

After Qt gates, rerun touched Go focused/full tests, build both Go commands,
then build and inspect the Ubuntu-compatible AppImage serially behind the
resource gate.

## Out Of Scope

- Server, Browser Controller, Agent UI/runtime, public Tunnel protocol or SSH
  changes.
- Per-session Terminal/Agent classification, identity reset/unpair, local
  permission policy, autoupdate, tray/background service installation.
- Desktop streaming/control.

## Acceptance

Task016 is review-ready when a non-root user can launch the GUI, start or attach
to the sibling daemon, obtain/copy/refresh a pairing code, observe connection
and sanitized activity, pause/resume joined control, close/reopen the GUI
without dropping the daemon, and run the resulting AppImage on the target
Ubuntu class with no secret artifact.

## Revision 1: Zero-Configuration Branded Start

Direct user feedback rejected exposing the Server Origin as a mandatory
first-run field. The branded AppImage must compile in a validated default HTTPS
origin, automatically start its sibling daemon when no local daemon is present,
and take the user straight to status/pairing. The origin remains build-time
overridable for self-host distributions, while an explicit advanced panel may
override it locally. Normal startup must not require visiting Settings.
