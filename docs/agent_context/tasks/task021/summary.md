---
task_id: task021
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 1
review_required: true
---

# Task 021 Summary: DSH Session Parity And Recovery

## Outcome

The five reported Controller defects are implemented, verified and deployed to
the bounded ASD test service at `https://122.51.70.33:10001`.

- Current Session permission is now an authoritative, configurable selector:
  `执行命令前询问` or risk-confirmed `完全访问`. Settings separately persists the
  default for future Sessions; it never rewrites an existing Session.
- DSH credential readiness is value-free. A missing DeepSeek key returns stable
  `PROVIDER_CREDENTIAL_REQUIRED` before a user message or Turn is persisted,
  shows a direct Settings action, and the same failed product/DSH Session works
  after configuration without replacement.
- Replayed transcript ordering is user -> reasoning -> tool -> final assistant,
  including equal timestamps. A single `折叠全部命令` action closes all expanded
  command cards without changing their state or output.
- Active Session rows expose separate Archive and confirmed Delete actions.
  Settings has a bounded owner-scoped archived list with Restore and confirmed
  permanent Delete. Archive is non-destructive; product deletion cascades its
  AISummoner message/tool projection. Provider-native transcript purge is out
  of scope and is not claimed.
- The Device Hub now exposes the same global Settings dialog. With no Device in
  scope it omits Device-only destructive controls while retaining General,
  Agent/default-permission and Session Management.
- Settings now preserves the user's selected tab across the 5-second Device
  status poll. A refreshed object for the same Device is no longer treated as
  a new Settings entry; only an actual entry-point change or loss of the Device
  scope can select a fallback section.

AISummoner remains the sole user, Device, approval, audit and Remote command
authority. “完全访问” only bypasses per-command confirmation for the reviewed
Remote execution capability; it does not grant DSH a local workspace sandbox,
filesystem or Server shell.

## Implementation

- Added forward migration `0002_agent_session_management.sql` for nullable
  `archived_at`, its management index and per-user future-Session defaults.
- Store queries use current Device ownership and non-revoked predicates in the
  same SQL statement. Active surfaces exclude archived Sessions; the global
  archived projection is newest-first and fixed at 50 rows.
- Permission update and Turn start contend on the same persisted Session state.
  A Turn either observes the committed policy after `BeginAgentTurn`, or the
  policy update loses with `TURN_IN_PROGRESS`.
- Archive/delete accept only idle/failed Sessions. The Service tombstone and
  hub boundary close stale subscribers and prevent lookup/admission races.
- DSH `credentials.describe` is projected to only `configured` and `writable`;
  preflight runs before reservation/message persistence. The Browser never
  receives the credential value or provider source.
- The final assistant projection is persisted at completion time and replay has
  a deterministic same-time priority. Reasoning, command lifecycle and final
  answer remain separate UI objects.

## Verification

All heavy work was serial. Go used `GOMAXPROCS=2`, `-p 1`; Web used one Vitest
worker and `NODE_OPTIONS=--max-old-space-size=2048`. No OOM occurred.

### Go

- Focused final tree:
  `go test -count=1 -p 1 ./internal/store ./internal/dsh ./internal/agent ./internal/agentapi ./internal/app ./cmd/aisummoner-server`
  — PASS (`0.079s / 0.120s / 1.662s / 5.798s / 0.069s / 3.335s`).
- Focused race:
  `go test -race -count=1 -p 1 ./internal/agent ./internal/agentapi`
  — PASS (`7.844s / 13.808s`).
- Full `go test -count=1 -p 1 ./...` — PASS, all 24 targets.
- Full `go vet ./...` — PASS.
- Server candidate build with real Web assets:
  `go build -trimpath -p 1 ./cmd/aisummoner-server` — PASS.

### Web

- `tsc --noEmit` — PASS.
- Focused 7-file suite — PASS, 42/42 tests.
- Revision 1 Settings/Workspace focus — PASS, 2/2 files and 17/17 tests.
- Full `npm test -- --run --maxWorkers=1 --reporter=dot` — PASS,
  16/16 files and 79/79 tests.
- `npm run build` with a 2 GiB heap — PASS, 73 modules. Assets:
  `index-B_GccOBp.js` (607,970 bytes) and `index-CADMNJga.css`
  (41,889 bytes). The existing >500 KiB chunk warning is non-blocking release
  debt; no memory gate was crossed.

## Honest Failure And Retry Record

1. The first isolated Go copy was truncated before `go.mod`/`internal`; no Go
   test started. A compressed complete snapshot fixed the staging transport.
2. The first real focused Go run exposed only new-test compatibility errors:
   the DSH test used the wrong fixture and an unavailable-provider test still
   expected the old asynchronous failure. Both were updated to the new
   preflight contract; the same six packages then passed.
3. The first selected Web run had six old expectations for the previous create
   body, Settings fetch set and Session-row query. They were updated without a
   production change.
4. A later selected run found two issues: a real transient/stale generic error
   around missing-Key recovery, and an archived-row text matcher. The product
   now suppresses the credential-caused historical generic error until the same
   Session successfully submits again; the matcher was narrowed. The 42-test
   rerun passed.
5. A latest-tree Go rerun caught one `racing`/`tracing` typo in the new test.
   Only the identifier was corrected; all six packages then passed.
6. The first full Web run found five pre-existing fuzzy Session-row queries now
   also matching Archive/Delete buttons. They were anchored to the Session row;
   the full 77-test rerun passed.
7. The initial deployment used `stop` then `start` on a transient systemd unit.
   `stop` unloaded the unit, so `start` returned “unit not found”. The rollback
   path had already restored the original binary and the database snapshot was
   complete. The exact original `systemd-run` properties were reconstructed;
   old health returned 200 before retry. The accepted switch used atomic binary
   replacement plus `systemctl restart`, which preserves the transient unit.
8. Two read-only evidence wrappers had quoting mistakes (`sed`, then a Python
   schema field expression) and stopped without mutation. Corrected value-free
   probes passed.
9. Revision 1 diagnosed the reported periodic Settings-tab reset as the Device
   poll returning a fresh object every five seconds while the reset effect
   depended on object identity. The first deployment staging attempt omitted
   the empty `web/` parent and stopped before Go compilation or production
   mutation. Creating that directory and copying the already verified `dist`
   completed the unchanged build path.

## ASD Deployment Evidence

- Running Server candidate SHA-256:
  `ee42761938da528858995cec9cffa6ab5d0b211ecf44a24e411f382c3e4bb18d`.
- Unit `aisummoner-task011-server-20260821T090612Z.service`: PID `1070610`,
  uid/gid `1001`, `NRestarts=0`, active/running since 12:07:01 +08:00.
- Public strict TLS health: HTTP 200, verify result 0. The index references the
  two new asset names. New Settings/DSH-status/archived routes each return the
  expected 401 before authentication.
- SQLite is still `0600`; migration 0002, `archived_at` and
  `agent_user_settings` were independently observed in read-only mode.
- Private listeners remain only `127.0.0.1:8088`, `:14196`, `:14197`; Caddy
  alone retains public TCP 10001.
- DSH credential status after restart is `configured=true`, `writable=true`,
  with no value/key/secret field in the private response projection.
- nginx's complete configuration manifest is byte-identical. Existing Caddy
  (`30fc8ec164a7`), OpenCode (`fc0120b460c9`) and the four unrelated containers
  retained their original IDs and were not restarted or edited.
- Recent Server records include ready and tunnel re-authentication, zero ERROR;
  the two WARN records are the expected tunnel closes across the controlled
  restart. Final ASD memory was about 4.45 GiB available and 1.95 GiB swap free.
- Exact rollback directory:
  `/home/myself/.local/state/aisummoner-task011/20260821T090612Z/rollback/task021-r1-20260824T040701Z`.
  It retains the immediately previous Task021 binary. The original Task021
  rollback directory continues to retain the stopped SQLite/WAL/SHM snapshot
  and pre-deploy nginx manifest.

## Core Source Hashes

- Migration `a028bf88...4bfac3e`; Store `0123d6d8...c4d3b`;
  Agent Service `495d364f...ad1d7`; DSH Adapter `3eb8eaa7...e9121`.
- Agent API `7ea26b24...e1f30`; responses `4a87a2c9...79c8d`;
  dispatcher `b10044f1...a1b71`; Server composition `ad931d13...8a5c`.
- Agent page `f7877e2d...a13fe`; replay `40c78405...d292`;
  Tool card `33605756...566a`; API client `1fce5705...832c`.
- Session rail `35f53631...1db1b`; Settings dialog `5cbc1310...b31c01f`;
  Settings regression `37e8b17b...06fc2a80`;
  Device Hub `bea0d179...49c5`; Workspace `6c0b54f9...74a97`;
  styles `5f084972...6b933`; README `dc34b936...63d9c`.

The full exact hashes of every corresponding test file were captured in the
implementation handoff command output; `git diff --check` is clean.

## Residual / Human Check

- The human tester accepted this revision as the first usable Controller
  milestone (“控制端初具雏形”) after confirming the Settings-tab regression.
  This records product acceptance only; it does not fabricate an independent
  code-review decision.
- An independent review remains required; none is fabricated because the user
  explicitly requested solo implementation without delegated agents.
- The deployed UI should receive one short human smoke: change the current
  Session to `完全访问`, run a benign command Turn without a confirmation modal,
  switch back to per-command, archive/restore one idle Session, and verify the
  Device Hub Settings entry on the tester's real browser.
- Provider-native DSH transcript deletion, Session rename/fork, richer provider
  adapters and code splitting remain later work.
