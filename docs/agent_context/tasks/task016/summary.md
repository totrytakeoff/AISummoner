---
task_id: task016
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 0
review_required: true
---

# Task 016 Summary: Qt Remote Client And AppImage

## Outcome

Task016 is implementation-complete and ready for independent review. The
controlled Linux device now has a Qt 6 Widgets desktop client with Status,
Events and Settings pages. It consumes Task015's private same-UID IPC and does
not own Device keys, Tunnel, SSH or remote commands. A sibling-only launcher
can start the Go daemon, while closing the GUI deliberately leaves that daemon
and remote connectivity running.

The GUI and the Go daemon are packaged together in one Ubuntu-compatible
AppImage. The final artifact was built in the pinned Ubuntu 22.04 environment,
its extracted AppDir passed the dependency/security contract, and the exact
AppImage passed a real local daemon/GUI/CLI lifecycle smoke.

An attempted final no-write Claude Code review timed out without output. This
is not represented as an approval; the task remains explicitly
`ready_for_review`.

## Files Changed

- `desktop/remote-client/CMakeLists.txt`, `src/*`, and
  `tests/test_remoteclientui.cpp` — the C++20 Qt application, strict IPC
  client, state/event models, daemon launcher, native Widgets UI, themes and
  tests.
- `deploy/RemoteClient.Dockerfile` — pinned Ubuntu 22.04 build of the static Go
  daemon and Qt GUI/test executable.
- `deploy/collect-qt-appdir.sh`, `check-remote-client-appdir.sh`, and
  `build-remote-client-appimage.sh` — Qt dependency collection, AppDir
  validation and reproducible AppImage assembly.
- `deploy/appimage-qt/*` — safe `AppRun`, desktop entry, icon and Qt plugin
  configuration.
- `.dockerignore`, `.gitignore`, `Makefile`, and `README.md` — bounded build
  context, artifact exclusions, build targets and end-user instructions.

## Behavior And Security

- `DaemonClient` creates one asynchronous `QLocalSocket` per request, caps
  newline-JSON frames at 64 KiB, applies bounded timers and never blocks the UI
  thread with `waitFor*`, sleeps or synchronous process execution.
- The parser rejects recursively duplicated keys (including escaped-key
  aliases), malformed/oversized frames, unknown fields, mismatched IDs or
  versions, invalid phases/events and response success/error ambiguity.
- Fixed daemon errors are mapped to fixed Chinese UI messages. Raw frames,
  endpoint values, pairing codes, commands, credentials and SSH material are
  neither logged nor persisted.
- Status polling is bounded to once per second. Event polling is incremental,
  non-overlapping and capped to 200 sanitized rows. A proven daemon restart
  clears the old cursor/model before fetching the new process's event stream.
- The Status page exposes explicit text and icons for every Core phase, Device
  ID copy, generic active-control count, pairing countdown/copy/expiry/refresh
  confirmation, pause/resume and recent sanitized activity.
- Settings persist only the HTTPS Server origin, bounded Device display name
  and system/light/dark theme. Active-daemon changes are truthfully labelled as
  applying on its next start.
- Service start verifies a real, non-symlink, non-group/world-writable sibling
  `aisummoner-client`, exact HTTPS origin and safe Device name. It launches
  `daemon` without a shell, root, development flags or caller-selected binary.
- The GUI refuses root. Closing it sends no daemon action. The detached daemon
  uses the stable user home/data directory, not the transient AppImage mount.
- Native window chrome, keyboard focus, accessible names, visible status text,
  confirmation dialogs, scrollable pages and light/dark neutral-blue themes
  are provided without WebEngine, QML, analytics, traces or screenshots.

## AppImage

- Artifact:
  `dist/AISummoner-Remote-0.1.0-x86_64.AppImage`
- Size: `34,761,208` bytes
- SHA-256:
  `3a548ac01e43c1b93ccd46811cef83e1d9e7dbca8d170415492b88d9a12df142`
- Checksum file:
  `dist/AISummoner-Remote-0.1.0-x86_64.AppImage.sha256`
- AppImage type: 2
- Extracted closure maximum required glibc symbol: `GLIBC_2.34` (within the
  Ubuntu 22.04 baseline).
- Default `AppRun` launches the Qt GUI. Explicit `--cli` safely forwards the
  remaining arguments directly to the bundled Go binary without shell
  evaluation.

Packaging used the official AppImage tools with recorded SHA-256 values:

- `appimagetool-x86_64.AppImage` —
  `ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0`
- `runtime-x86_64` —
  `2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d`

## Verification

- Local Qt build in a fresh build directory: PASS.
- Local offscreen QtTest: PASS — `12 passed, 0 failed, 0 skipped`, about
  `2.263s`. The only output was the expected offscreen platform
  `propagateSizeHints()` warning.
- Pinned Ubuntu 22.04 Docker compile and CTest after final production changes:
  PASS — one CTest target, `2.35s`, Qt 6.2/GCC 11.
- Production C++ warning gate (`-Wall -Wextra -Wpedantic`): PASS, no warnings.
- Focused `clang-tidy` analyzer/bugprone/performance pass over nine production
  `.cpp` files: PASS, no diagnostics after excluding non-actionable enum-size,
  easily-swappable-parameters and reserved-identifier checks.
- Shell syntax for all packaging scripts: PASS.
- Extracted AppDir contract: PASS — required Qt platform/image plugins,
  `AppRun`, desktop metadata, recursive runtime dependencies and safe modes;
  no unresolved libraries, bundled glibc/graphics-driver system libraries,
  dynamically linked Go daemon or world-writable regular files/directories.
- Exact final-artifact lifecycle smoke: PASS. The packaged daemon started on
  an unreachable loopback HTTPS origin, the packaged GUI attached to its
  same-UID socket, GUI exit left the daemon alive, packaged CLI
  pause/status/resume worked, daemon termination removed its socket, and
  direct Type-2 offscreen launch remained alive until the expected bounded
  timeout (`124`) with no startup error.

No Task016 validation changed or restarted ASD services. All heavy Qt/AppImage
work ran locally or inside the dedicated local Docker build.

## Honest Failure And Retry Record

1. The first local compile exposed a lambda const-correctness error; the
   capture was corrected before any verification claim.
2. The first QtTest run had `10` passes and one failure because a settings test
   counted INI newlines. It was replaced with an exact persisted-key whitelist.
3. A temporary Xvfb screenshot attempt could not enumerate the root window.
   Its exact processes were killed; visual inspection used a dedicated
   renderer and no screenshot became a product/test artifact.
4. The first Ubuntu Docker compile lacked OpenGL development headers; the
   pinned build image now installs `libgl-dev`.
5. Successive first AppDir attempts exposed three packaging-script defects:
   `qtpaths6` was absent from PATH, `/usr/bin/qtpaths` was a qtchooser wrapper,
   and a static daemon was passed to `patchelf`. Resolution now prefers
   `/usr/lib/qt6/bin/qtpaths6` with a qmake fallback and probes rpath capability
   before patching.
6. The first AppDir mode check treated normal `0777` symlink metadata as a
   world-writable payload. The contract now checks only regular files and
   directories.
7. An initial local rebuild reused a Makefiles directory with the Ninja
   generator. Final local verification used a new isolated build directory.
8. The first `clang-tidy` call enabled no checks; the next inherited one
   GCC-only argument and found low-risk mechanical warnings. The final explicit
   command removed that argument, fixed the warnings and passed cleanly.
9. Self-review found that an IPC daemon restart could reuse a stale event
   cursor. The client now detects disconnect/recovery, clears the model/cursor
   and refetches; a deterministic real-`QLocalServer` restart regression was
   added, after which all Qt, Docker and artifact gates were rerun.
10. Two final no-write Claude Code attempts produced no usable verdict (one
    empty completion and one bounded 180-second timeout). No external approval
    is inferred or fabricated.

## File Hashes

### Qt source and tests

- `desktop/remote-client/CMakeLists.txt` — `378ef2b4e2c7a6ed3a6bec07a0941420744373981ee9fb54485335365530131b`
- `src/appsettings.cpp` — `1a1e0d85ea5b6a94fbbed0b0a65ab7eade300d1f1cd3549627d4833e2ed16ea0`
- `src/appsettings.h` — `721f894d15e4a9d6943c101a4aa45564cf05535a2344251e4a5434b4ef8ffe8d`
- `src/daemonclient.cpp` — `941dab90770b89b78ef6d5aeefcbacd7652acd3e973882725ff8df7afbd4945b`
- `src/daemonclient.h` — `ad005caa573da0f2bfced4b859f43ec8c1689dd76b25557f4b4529e90280975b`
- `src/daemonlauncher.cpp` — `3b2e8fe5edafcb1a7076d5011a27ec2526a318855cd859778766667cefb56571`
- `src/daemonlauncher.h` — `12d2d1f9cf88035758f082d3d79a78f171d1351384b54218b0f83490a8bdb5f6`
- `src/eventmodel.cpp` — `e6c75bef4b3785bce986282b6e77db07449989800a31e907164b36abc05fcff7`
- `src/eventmodel.h` — `2ef474a7334ea97b9d41a86156aa423c2071233af8105db121799966244cca5c`
- `src/main.cpp` — `80f120e070ef39cbafadb4cd7c7720c11bd2aaa9c53568ec264458bc119fe3da`
- `src/mainwindow.cpp` — `78a9db97820e513e6ee04b4fdfde13b242ec2b75af2690e8f47d41844065e03e`
- `src/mainwindow.h` — `e15e546b469c29974280185e328c5dbc89c0913124b9ad2fbc5a8dca7f21d966`
- `src/models.cpp` — `15d97c1319e9dd3660001cfe86075d35231df84331dd05b2957911862b973d16`
- `src/models.h` — `6d54fa947b29805d14ce0bd441fa3677e9c3a7cba7dc0be78781acff605f43d6`
- `src/strictjson.cpp` — `a29f9f0c47b6d7667d07195326b3d2e8ecf5189c46c85839abeafc49599c87bf`
- `src/strictjson.h` — `7f2b01863bda2c84aaeeb16c2ee9fef124155a25e84ef68a78f988fcc6a10b9c`
- `src/theme.cpp` — `6e4e0960872549393fa4a4e2a77a997e484a660e70e08d4eb5c7784f14b4a261`
- `src/theme.h` — `1dc7ac0804b891de07d5689d710e90ee07a8c41826d39514bcd43d4216b5fa81`
- `tests/test_remoteclientui.cpp` — `919ed4780a704c16ab1ef3fdf84e4946e9ca904d46820b16ba9ee6b12bfc599f`

### Packaging and user-facing integration

- `deploy/RemoteClient.Dockerfile` — `1090876f2451b999092d066c93ea58b5f73b6b078d682382e552094c037b7de6`
- `deploy/build-remote-client-appimage.sh` — `822e709c615fbf225d009a8749f597942dc54af6c68bfe3d52725bcdf9b2035d`
- `deploy/check-remote-client-appdir.sh` — `0ed2023b5bbf67f6c41e65c2bbadd43147419fb642723b6c765808ffbe341c60`
- `deploy/collect-qt-appdir.sh` — `34e652c4acd22d2a8e198992c20251df027db8189625b52559dc0d06b1e4cc85`
- `deploy/appimage-qt/AppRun` — `fe93a7dcc6cc462cf5c4e220493dfb6eab18d526a95034ff4eb4e92406a58b7d`
- `deploy/appimage-qt/aisummoner-remote.desktop` — `3cfc2d2a97292360baa3537e9f518addae84d581dec12d6ea1863c7141a1e61f`
- `deploy/appimage-qt/aisummoner-remote.svg` — `e52df43520a2ff5700876542227aef8eb1c5274294779ac6376843ad40b94c0a`
- `deploy/appimage-qt/qt.conf` — `b0bde058f7cd8aee5ad86e6136822349272d1fe9785b8b8dc436d22fb02111ca`
- `.dockerignore` — `a00bc700df2d9c48e38af35d301a4aa5eeb5f2dcbfef6b68d2ffbbaf05217ff3`
- `.gitignore` — `308f6a6e1594736ad50abde08d6711a437014e0b9e0ed987a11cbe9bc8e11634`
- `Makefile` — `918c356f70ea136c76aefec70826101d2a4d3ba2c34e6d4bed9d7d6166c49c3d`
- `README.md` — `b9c6accdec9769a44f756d0aa06eadddcfb36834c01ac1d48ff8f9876edd7b62`
- checksum file — `14892fd86dacc0cfa24db5aac26f23d2e3d5a0d8b99b1bfab210304cd7ad35c2`

## Residuals And Handoff

- Independent combined Task015/Task016 review is still required. The external
  reviewer timeout is evidence of unavailability, not approval.
- The exact AppImage has passed local Ubuntu-class packaging and lifecycle
  smokes, but a human non-root run on the user's target Ubuntu desktop remains
  the final distribution/visual acceptance step.
- IPC v1 reports a generic active SSH control-session total only. The GUI does
  not invent Terminal-versus-Agent classifications.
- Running-daemon endpoint/name changes remain next-start settings; hot
  reconfiguration and local permission management are intentionally deferred.
- The current ASD deployment is untouched and remains an independent
  rollback/test environment.
