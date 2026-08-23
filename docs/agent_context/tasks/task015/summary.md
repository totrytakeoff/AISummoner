---
task_id: task015
type: summary
status: ready_for_review
from: implementation
to: reviewer
revision: 0
review_required: true
---

# Task 015 Summary: Remote Core Daemon And Private IPC

## Outcome

Task015 is implementation-complete and ready for independent review. The Linux
Remote Client now has a reusable, UI-independent Go Core around the existing
Device identity, outbound Tunnel and embedded SSHD. It publishes immutable
status, a bounded sanitized event history, pairing expiry and a generic active
SSH control-session count.

The new `daemon` command serves a versioned same-UID Unix-socket API. Local
`status`, `pause`, `resume` and `refresh-pairing` commands consume that API;
legacy `start` remains available and is still the only command that prints a
pairing code to interactive stdout. The systemd unit now uses daemon mode and
does not create the old pairing-output file. Task016 can build the Qt GUI
against the frozen IPC schema without owning Tunnel/SSHD lifecycle.

Before formal review, a no-write Claude Code adversarial pass reported no
blocker. The implementation owner then found one legacy-only blind spot that
the review missed: writing a pairing offer directly from the Tunnel callback
could let stdout backpressure delay Tunnel cancellation. Pairing delivery is
now a capacity-one latest-value Core channel; the callback only performs a
bounded memory update and legacy stdout runs outside the Tunnel call stack.

## Files Changed

- `internal/remoteclient/controller.go`, `controller_test.go` — single-use Core,
  explicit phase/snapshot, joined pause/resume/refresh, pairing timer, bounded
  event ring and concurrent stream accounting.
- `internal/clientipc/protocol.go`, `server.go`, `client.go`, `server_test.go` —
  newline JSON v1, duplicate/unknown-field rejection, mode-0600 same-UID Unix
  socket, fixed errors, bounded handlers/deadlines and local Go client.
- `internal/tunnel/client.go`, `client_test.go` — safe phase and generic stream
  callbacks without changing Tunnel wire protocol.
- `cmd/aisummoner-client/main.go`, `main_test.go` — legacy foreground, daemon,
  four local control commands, private socket validation and a real Core→IPC
  composition regression.
- `deploy/aisummoner-client.service`, `internal/app/deploy_contract_test.go` —
  daemon-mode service, null stdout and removal of the pairing-output contract.
- `README.md`, `docs/design/remote-client-ipc-v1.md` — CLI/systemd instructions,
  exact Qt-facing schema, error codes and secret exclusions.
- `docs/design/remote-client-qt.md` — Task016 UI input distilled from a
  no-write Claude Code design review and the accepted AISummoner boundaries.

## Behavior And Security

- The Core loads the existing private Device identity, keeps the exact
  Ed25519/embedded SSHD path and opens no TCP listener.
- Phases are `starting`, `connecting`, `online`, `retrying`, `paused`,
  `stopped`, `error`. Pause and parent shutdown wait for the Tunnel and every
  accepted stream before completion; resume never overlaps two runners.
- Refresh is accepted only while an active or expired offer exists. It clears
  the old offer, joinedly cycles the Tunnel and returns the fixed
  `closes_active_sessions=true` contract for Qt confirmation.
- At most 200 fixed-summary events are retained. Serialized events exclude
  pairing code, Server URL, command/cwd, Terminal/Agent content, SSH material,
  credential, raw transport errors and request payloads.
- Legacy pairing notification uses a non-blocking capacity-one channel that
  replaces an unread old value with the latest offer. stdout/UI backpressure
  cannot enter the Tunnel callback or hold its joined cleanup.
- IPC requires an absolute socket inside the absolute private data directory.
  The parent is a same-owner, non-symlink mode-0700 directory; the socket is a
  same-owner, non-symlink mode-0600 socket. Linux `SO_PEERCRED` must match the
  daemon UID before any request bytes are read.
- Frames are one strict JSON object plus newline, at most 64 KiB. Unknown and
  recursively duplicated keys, multiple values and malformed types are
  rejected. There is one request per connection, at most eight handlers, a
  five-second operation deadline and a bounded 250ms error-write grace.
- Only `status.get` can serialize a current pairing code. Fixed errors and
  daemon logs never reflect request, endpoint, credential or pairing values.
- Daemon/root/TLS restrictions are the same as legacy start. Local control has
  no Server credential or remote-access surface.

## Verification

All Go work ran serially in isolated ASD `/tmp` staging after checking memory,
swap and absence of competing Go/Node jobs. No ASD service, container, listener
or persistent path was changed.

Final evidence:

- Final focused four packages: PASS — `remoteclient 0.038s`, `clientipc 0.045s`,
  `tunnel 1.737s`, `cmd/aisummoner-client 0.008s`.
- Core/IPC `-count=10`: PASS — `0.323s / 0.410s`.
- Concurrent pause/resume/refresh runner oracle `-count=20`: PASS — `0.047s`;
  maximum live runner remained exactly one and final count zero.
- Non-blocking/latest pairing notification `-count=100`: PASS — `0.007s`.
- Final focused race four packages: PASS — `1.090s / 1.063s / 9.831s / 1.024s`.
- Clean-tree `GOMAXPROCS=2 go test -count=1 -p 2 -timeout 600s ./...`:
  PASS for all 27 test package targets plus migrations (`no test files`).
- `GOMAXPROCS=2 go vet ./...`: PASS, no output.
- `GOMAXPROCS=2 go build -trimpath -p 2 ./cmd/aisummoner-server
  ./cmd/aisummoner-client`: PASS, no output.
- Final pre-handoff clean implementation snapshot: 1008 files / 95 Go files,
  remote `gofmt -l`
  empty, local/ASD aggregate SHA-256
  `13adef9e830e678dc99edd16222451fd30d04a6ea7720d119d4261252f1990df`.
- Final ASD state: MemAvailable `4,717,652 kB`, SwapFree `1,999,924 kB`, no
  Go/compile/link/Node jobs. All three exact Task015 staging directories were
  deleted and confirmed absent.

## Honest Failure And Retry Record

1. The first focused run found the new defensive snapshot test injecting an
   online callback before `Run`; production correctly ignored it. The test was
   changed to start a real fake runner before injection; the same four packages
   then passed (`0.035/0.011/1.735/0.004s`).
2. One resource gate matched its own shell command text and exited before Go
   started. Subsequent gates match exact process names only.
3. The new deadline test exposed a real response race: operation context and
   socket write deadline expired simultaneously, so callers could receive an
   invalid response instead of fixed `TIMEOUT`. The socket now retains a
   bounded 250ms response grace; the focused IPC package passed (`0.046s`). One
   tool call lost this PASS output, so the exact test was mechanically rerun
   with an on-staging result file rather than inferred.
4. While adding the refresh result flag, a mechanical patch initially returned
   that schema from `daemon.pause` instead of `pairing.refresh`. IPC and real
   Core composition tests both failed. The branches were corrected and the
   full focused/race sequence was rerun.
5. The first clean-tree full test had 26 test-package targets pass and only
   `internal/app/TestDeploymentContractSources` fail because it still demanded
   the obsolete pairing-output unit. The oracle was updated to require daemon,
   null stdout and forbid the old file/start command; focused app and the exact
   full-tree command then passed.
6. A first no-write review returned APPROVED, but the implementation owner did
   not accept it blindly: a follow-up trace found the legacy pairing stdout
   write still inside the Tunnel callback. It was replaced with the bounded
   latest-value channel, then focused, 100-repeat, race, full-tree, vet and both
   command builds were all rerun before this handoff.

## File Hashes

- `internal/tunnel/client.go` — `1552a2391ef38918c03b885f1fbc63b1504080b636453515cdb1b5d3f849ad94`
- `internal/tunnel/client_test.go` — `41fe85af2cd43ce4adb9e56ba058e5c5fd6943b242c794b0bf0bfcf913e77b2f`
- `internal/remoteclient/controller.go` — `e4210369f2002743799045326ddfea434a79c64ef85e6794ea60ceddfabd863b`
- `internal/remoteclient/controller_test.go` — `d9c19d1aded4470e6265ee659ebe5a18eb5b5324f227ac530c978133d5f74947`
- `internal/clientipc/protocol.go` — `a183f8238110039c698b1bf9c4e4a991e82492433bc6bb905fbdb55b6f26370c`
- `internal/clientipc/server.go` — `c5e699ca54e4da0bf45c082145e63681db69fc15fbf01adf6621a35b36b5b32b`
- `internal/clientipc/client.go` — `f8ad73b6710bebb0f6e6003e89742fdfd192f1772096af2aea3e2d28a2c7f33f`
- `internal/clientipc/server_test.go` — `2d53a45a3079579011288d0d20195278812580e48221962c21947354884c6916`
- `cmd/aisummoner-client/main.go` — `ce785d507ac27ac4ffd1540a568dfe5e0a7e1ebce1efdb64597a3b698bdb5c91`
- `cmd/aisummoner-client/main_test.go` — `aa2a6f939084502fa8bda932f9f2dd017f18b0a6cdb77f920fa806224d230912`
- `deploy/aisummoner-client.service` — `b7ef85de3673dd0893d4bf51604689da65573416d2bede41ca5d2de1013e7b45`
- `internal/app/deploy_contract_test.go` — `caad475eb6ece602392d2918d2c47e9968c51e8bbaaf7317236bb3018b0169d2`
- `README.md` — `df819176d536c2581e22826dc8e8536783c6d13bd04f39d7878a070b9485e46b`
- `docs/design/remote-client-ipc-v1.md` — `cd3567a85957b28ec28beac65de6ecbfdaf7210cd11c5a050b5bdff46f203ea6`
- `docs/design/remote-client-qt.md` — `2b508ea220f6efd808f33c26c6871a1e1716f3c24254cfa3bd3dac829de70b24`

## Residuals And Task016 Handoff

- Current Tunnel v1 reports only generic SSH control streams. Qt must not label
  them Terminal versus Agent until a separately reviewed protocol revision.
- Server/device settings remain read-only in IPC v1. Task016 may display them
  but must not pretend it can hot-reconfigure the running Core.
- Qt/AppImage source, startup recovery and real Ubuntu GUI validation remain
  Task016. Closing the GUI must not send `daemon.pause`.
- The old CLI AppImage remains a rollback/test artifact until the Qt AppImage
  passes independent review and non-root E2E.
