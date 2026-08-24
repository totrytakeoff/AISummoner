---
task_id: task022
type: summary
status: ready_for_review
from: coder
to: reviewer
revision: 0
review_required: true
---

# Task 022 Summary: DSH Provider And Model Configuration

## Outcome

DSH is no longer treated as a single hard-coded official DeepSeek client. The
Controller can now manage DSH-native provider profiles and select the current
Session's provider, model and advertised reasoning effort. The implementation
is verified and deployed to the bounded ASD test service at
`https://122.51.70.33:10001`.

- Settings now lists active/configured and dormant DSH providers, edits an
  existing provider, materializes a built-in provider, adds/removes a custom
  route, and manages an optional write-only API key.
- The Agent composer shows the actual current model. Its grouped selector uses
  DSH's catalog and exposes only the reasoning efforts advertised for that
  exact model; a change applies to the same product/DSH Session.
- Turn preflight checks the selected route and its current value-free
  credential status instead of assuming `DEEPSEEK_API_KEY`. Missing credentials
  and removed routes remain recoverable without replacing the conversation.
- Generic optional Runtime-configuration and Session-model capabilities are
  now available to later OpenCode, Codex and Claude Code adapters. Their config
  files and current behavior were deliberately not changed in this task.

## Architecture And Security

- DSH remains the fact source through `llm.providers`, `settings.describe`,
  revision-checked `settings.mutate`, `credentials.describe/set/unset`,
  `session.models` and `session.selectModel`.
- AISummoner persists only its existing opaque external DSH Session ID. It does
  not copy provider profiles, catalogs or credentials into SQLite.
- Browser responses contain curated provider/model fields and only
  `configured`/`writable` credential booleans. Credential references, sources,
  values, raw settings schemas/documents and private RPC diagnostics do not
  cross the Server boundary.
- Provider writes use DSH namespace revisions. Existing unknown fields survive
  path-addressed mutations. Removal commits the revision-checked settings
  change before clearing only a conventionally owned credential.
- HTTPS provider URLs are accepted; plain HTTP is restricted to a literal
  loopback address. IDs, strings, model counts, JSON requests and DSH responses
  are bounded.
- Model preparation/selection and Turn admission are mutually exclusive for
  one product Session. A Turn sees a completed selection or returns
  `TURN_IN_PROGRESS`; it cannot consume half-applied state.
- `PUT` joined POST/PATCH/DELETE under the existing exact-Origin guard. All new
  endpoints also require the existing authenticated owner/capability boundary.

The future configuration-file rules and the additive OpenCode versus
switch-oriented Codex/Claude distinction are frozen in ADR-0006. The reference
checkouts were read-only and pinned at:

- DeepSeek Harness: `47f943859bef60e4160492346772ded9b24f765a`;
- CC Switch: `9a596158ca926e74b56243c08af67d9dd13fc27c`.

## Verification

All heavy work was serial and bounded. Go used `GOMAXPROCS=2` and `-p 1`; Web
used one Vitest worker and a bounded Node heap. No OOM occurred.

### Go

- Focused Agent/DSH/API/composition suites — PASS.
- Full `go test -count=1 -p 1 -timeout 600s ./...` — PASS, all 24 test targets.
- Race runs for `internal/dsh`, `internal/agent` and `internal/agentapi` — PASS.
- Full `go vet ./...` — PASS.
- Final `CGO_ENABLED=0 GOMAXPROCS=2 go build -trimpath -p 1
  ./cmd/aisummoner-server` — PASS; `file` reports statically linked and `ldd`
  reports no dynamic executable.

### Web

- Targeted provider/model/Settings/Workspace suites — PASS, 36 tests.
- Full one-worker suite — PASS, 16/16 files and 82/82 tests.
- Production build — PASS. Embedded assets are `index-e8Hp9xK2.js`
  (620,938 bytes) and `index-B4FI4e_F.css` (47,550 bytes). The existing
  >500 KiB JavaScript warning remains non-blocking bundle-splitting debt.

### Live Contracts

- Direct pinned DSH provider-directory contract against
  `http://127.0.0.1:14196` — PASS without a settings or credential mutation.
- Authenticated public smoke — login 200, redacted provider directory 200 with
  37 routes, devices 200 with two existing Devices, current Session models 200,
  and an idempotent select of the already-current provider/model/effort 200.
  The temporary Cookie was logged out and deleted.
- The public unauthenticated matrix is exact: health 200 with TLS verify result
  0; provider GET 401; provider PUT without Origin 403; provider PUT with exact
  Origin but no Cookie 401; Session-model GET 401. Both new assets return 200.

## Honest Failure And Retry Record

1. The first full Go run exposed a README deployment-contract assertion: the
   public docs had only one private `.env` instruction. A bounded Compose
   example was added and the complete suite passed.
2. Focused API security tests caught that the existing Origin guard omitted
   `PUT`. The guard and its regression matrix were corrected before release.
3. Fixture/review passes caught DSH model-setting wire casing, normalized
   reasoning-effort replies, common input bounds and custom-route credential
   removal ordering. Each issue was fixed before the full gates.
4. The first deployed Task022 candidate was healthy but the new public paths
   returned 404 because the top-level Server dispatcher did not recognize the
   new exact path shapes. Exact positive/negative dispatcher tests were added;
   the corrected candidate returned the expected 401/403 matrix.
5. One read-only status probe queried `systemctl --user` and misleadingly
   reported the unit absent. `/proc` cgroup evidence showed the Server had
   remained active under the system transient unit; no service mutation
   occurred before switching to the correct scope.
6. The first corrected r1 build inherited ASD's `CGO_ENABLED=1` and linked to
   host libraries. Final release inspection caught the mismatch. The identical
   tree/assets were rebuilt with `CGO_ENABLED=0`, proven static, atomically
   deployed as r2 and fully re-smoked.

## ASD Deployment Evidence

- Final running Server SHA-256:
  `9e88b2d252ee899ae3209076d485d1488a38c57634ef08430a850857bfd27409`
  (19,312,798 bytes, static x86-64 ELF).
- Unit `aisummoner-task011-server-20260821T090612Z.service`: PID `1306920`,
  `NRestarts=0`, active/running. Server and private DSH child remain on
  `127.0.0.1:8088`, `:14197` and `:14196`; OpenCode remains on `:14096`;
  Caddy alone retains public TCP 10001.
- nginx's 14-file manifest is 14/14 byte-identical. Existing Caddy
  (`30fc8ec164a7`), OpenCode (`fc0120b460c9`) and all four unrelated container
  IDs are unchanged.
- Final scoped logs contain zero ERROR/WARN. The service cgroup peak was about
  206 MiB; ASD retained about 4.2 GiB available RAM and 1.9 GiB free swap.
- Rollback chain:
  `task022-20260824T113927Z` retains the previous Task021 binary;
  `task022-r1-20260824T1145Z` retains the first Task022 candidate; and
  `task022-r2-20260824T1151Z` retains the dynamic r1 candidate.

## Core Source Hashes

- Agent types `281803f0...04c344`; Service `af443e8c...7685bc`;
  Agent API `131daf43...18cf9`; responses `29c24530...166a5d`;
  dispatcher `3db352fe...54e564`.
- DSH Adapter `0175fde0...a61521`; DSH configuration
  `4c45f5fd...54071f`.
- Web API client `ce308851...e393a`; Web API types
  `8fce0363...5f685`; provider Settings `ddfd384f...45fd25`;
  Agent page `e824e6f5...b165b0`; styles `5d6e1c0a...ef4685`.
- ADR-0006 `3aa6ff75...6456a`; README `b4460900...c686c`.

## Residual / Human Check

- No live custom route or credential was written during automated deployment
  validation, so the user's existing DSH configuration remained untouched.
  The human smoke should add the intended third-party endpoint/key in
  `设置 -> Agent 与模型 -> 模型供应商`, select its model in the composer, run one
  benign Turn, then switch back in the same conversation.
- A real LLM -> Remote command Turn was not repeated automatically because it
  could consume paid inference and operate the currently connected Device. The
  unchanged DSH Capability Bridge/Remote chain was already proven in Task020;
  the selected-route preflight and model path now need the above human smoke.
- Independent review remains required. None is fabricated because the user
  explicitly requested that this refactor be implemented without subagents.
