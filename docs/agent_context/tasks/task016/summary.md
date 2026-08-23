---
task_id: task016
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 1
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

Revision 1 incorporates direct user feedback about first-run friction. The
branded build now embeds the current publicly trusted AISummoner HTTPS origin,
automatically starts the sibling daemon after the first unavailable IPC probe,
and takes a normal user directly to connection/pairing. Server configuration is
hidden under an explicit advanced self-host panel and remains build-time
overridable for other deployments.

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
- Settings persist only an optional advanced HTTPS Server override, bounded
  Device display name and system/light/dark theme. An empty/legacy value
  migrates to the branded default. Active-daemon changes are truthfully
  labelled as applying on its next start.
- The first unavailable IPC result is announced once and triggers exactly one
  automatic daemon launch. Normal startup never requires visiting Settings;
  manual retry remains available after a real launch failure.
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
- Size: `34,765,304` bytes
- SHA-256:
  `7761bad68048d5ad64378a44d633f88de4d1625dae191266287e8d3f3f8f5d42`
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
- Revision 1 local offscreen QtTest: PASS — all tests passed, `2.28s`. This
  includes default-origin loading, exact one-shot automatic daemon start and
  advanced-panel-hidden assertions. The only non-test output was the expected
  offscreen platform
  `propagateSizeHints()` warning.
- Pinned Ubuntu 22.04 Docker compile and CTest after revision 1: PASS — one
  CTest target, `2.37s`, Qt 6.2/GCC 11.
- Production C++ warning gate (`-Wall -Wextra -Wpedantic`): PASS, no warnings.
- Focused `clang-tidy` analyzer/bugprone/performance pass over nine production
  `.cpp` files: PASS, no diagnostics after excluding non-actionable enum-size,
  easily-swappable-parameters and reserved-identifier checks.
- Shell syntax for all packaging scripts: PASS.
- Extracted AppDir contract: PASS — required Qt platform/image plugins,
  `AppRun`, desktop metadata, recursive runtime dependencies and safe modes;
  no unresolved libraries, bundled glibc/graphics-driver system libraries,
  dynamically linked Go daemon or world-writable regular files/directories.
- Revision 0 artifact lifecycle smoke remains PASS: GUI attach/close left the
  daemon alive, packaged CLI pause/status/resume worked, daemon termination
  removed its socket, and direct Type-2 offscreen launch had no startup error.
- Exact revision 1 AppImage live start: PASS. With an existing empty settings
  value, the GUI migrated to the branded default, automatically launched the
  sibling daemon, connected through strict public TLS, reached `online`, and
  exposed a present, unexpired pairing offer. The socket was mode `0600` and
  owned by the desktop user. Verification output contained only booleans, not
  the pairing code.

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
11. Revision 1 initially declared its Docker build argument before the Ubuntu
    dependency layer, unnecessarily invalidating the cached 590 MiB Qt toolchain
    and starting a very slow mirror download. That in-progress, artifact-free
    build was explicitly canceled; the argument was moved below the dependency
    layer, and the unique restart reused the verified cache, compiled, tested
    and packaged successfully.

## File Hashes

### Qt source and tests

- `desktop/remote-client/CMakeLists.txt` — `98a54082dee2d33d79188bd59f542f57670d0895c5e95de57d4857c0758ddd86`
- `src/appsettings.cpp` — `39e8a830a41250ba310eec1c23c56d1d23a792aa6b829b05657185ba5d00f0f3`
- `src/appsettings.h` — `d23a6ba801bbf2caf599f5aa00ff578829db70fe36e736ec1e7f59d629ab1601`
- `src/daemonclient.cpp` — `1cbd032abc50140659eb95861d48998297b8cd886c835a8883d2c0978758498d`
- `src/daemonclient.h` — `c7d0ec577936bc1653bef51b87156bfa85c502cf80d06be941c588c17f7600a7`
- `src/daemonlauncher.cpp` — `3b2e8fe5edafcb1a7076d5011a27ec2526a318855cd859778766667cefb56571`
- `src/daemonlauncher.h` — `12d2d1f9cf88035758f082d3d79a78f171d1351384b54218b0f83490a8bdb5f6`
- `src/eventmodel.cpp` — `e6c75bef4b3785bce986282b6e77db07449989800a31e907164b36abc05fcff7`
- `src/eventmodel.h` — `2ef474a7334ea97b9d41a86156aa423c2071233af8105db121799966244cca5c`
- `src/main.cpp` — `80f120e070ef39cbafadb4cd7c7720c11bd2aaa9c53568ec264458bc119fe3da`
- `src/mainwindow.cpp` — `582d1b922e7462dcc7340e695e7101f85a7d7ff1cec24de96ab5b8f6a9c4c4d5`
- `src/mainwindow.h` — `68e8bfe9982c84d81916739be4f28e23f2cc3fd18a0b226c42668836de2adee5`
- `src/models.cpp` — `15d97c1319e9dd3660001cfe86075d35231df84331dd05b2957911862b973d16`
- `src/models.h` — `6d54fa947b29805d14ce0bd441fa3677e9c3a7cba7dc0be78781acff605f43d6`
- `src/strictjson.cpp` — `a29f9f0c47b6d7667d07195326b3d2e8ecf5189c46c85839abeafc49599c87bf`
- `src/strictjson.h` — `7f2b01863bda2c84aaeeb16c2ee9fef124155a25e84ef68a78f988fcc6a10b9c`
- `src/theme.cpp` — `6e4e0960872549393fa4a4e2a77a997e484a660e70e08d4eb5c7784f14b4a261`
- `src/theme.h` — `1dc7ac0804b891de07d5689d710e90ee07a8c41826d39514bcd43d4216b5fa81`
- `tests/test_remoteclientui.cpp` — `1263215c808d3f676049de6db98db13ef9bc03e0641623cdc61f2fa78d21781e`

### Packaging and user-facing integration

- `deploy/RemoteClient.Dockerfile` — `4f2da8dfeaf5d501939d5f0f2817654627150c466b81b8b0df1f1db4709aa7e3`
- `deploy/build-remote-client-appimage.sh` — `0094f4900e86cea8b8f9e415476796ddd4d865f4f01b7ce4fe8a697e098b04f0`
- `deploy/check-remote-client-appdir.sh` — `0ed2023b5bbf67f6c41e65c2bbadd43147419fb642723b6c765808ffbe341c60`
- `deploy/collect-qt-appdir.sh` — `34e652c4acd22d2a8e198992c20251df027db8189625b52559dc0d06b1e4cc85`
- `deploy/appimage-qt/AppRun` — `fe93a7dcc6cc462cf5c4e220493dfb6eab18d526a95034ff4eb4e92406a58b7d`
- `deploy/appimage-qt/aisummoner-remote.desktop` — `3cfc2d2a97292360baa3537e9f518addae84d581dec12d6ea1863c7141a1e61f`
- `deploy/appimage-qt/aisummoner-remote.svg` — `e52df43520a2ff5700876542227aef8eb1c5274294779ac6376843ad40b94c0a`
- `deploy/appimage-qt/qt.conf` — `b0bde058f7cd8aee5ad86e6136822349272d1fe9785b8b8dc436d22fb02111ca`
- `.dockerignore` — `a00bc700df2d9c48e38af35d301a4aa5eeb5f2dcbfef6b68d2ffbbaf05217ff3`
- `.gitignore` — `308f6a6e1594736ad50abde08d6711a437014e0b9e0ed987a11cbe9bc8e11634`
- `Makefile` — `548ed862585a6b4df8842ef1d0acee002fbf4f1e32fc1ad888e717c4396fcfbb`
- `README.md` — `93567f2e66714e7f4d9f332965ebc49fad1ad5bf80e67f111a6bf66964b4fc0b`
- checksum file — `1782b9133bec809cd8901ecf62a267ab81bd9d29019cf81021f7022b246338c9`

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
