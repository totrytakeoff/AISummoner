---
task_id: task022
type: plan
status: ready_for_review
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 022 Plan: DSH Provider And Model Configuration

## Status

Implementation authorized. This task freezes the common Runtime configuration
boundary and implements only the DSH provider/model slice. OpenCode, Codex and
Claude Code keep their current behavior.

## Owner

Primary implementation agent, working without delegated coding agents at the
user's request.

## Reviewer

Independent review remains release debt. No delegated reviewer is fabricated
because the user explicitly requested solo implementation for this refactor.

## Context And Prior Art

The current Controller writes only `DEEPSEEK_API_KEY` through DSH
`credentials.set`, checks that one reference through `credentials.describe`,
and exposes no model selector. This incorrectly treats the DSH Runtime as a
single official-provider API client.

The pinned DSH checkout
`47f943859bef60e4160492346772ded9b24f765a` already owns the required native
configuration plane:

- `llm.providers` joins configurable routes with live/dormant state;
- `settings.describe` and revision-checked `settings.mutate` expose redacted,
  hot-applied `llm-deepseek` and `llm-pi-ai` profiles;
- `credentials.describe/set/unset` keep values write-only;
- `llm.models`, `session.models` and `session.selectModel` provide grouped
  catalogs and per-Session provider/model/reasoning selection.

The CC Switch reference is pinned for this design review at
`9a596158ca926e74b56243c08af67d9dd13fc27c`. Its reusable lesson for future
configuration-file Runtimes is transactional ownership, not its app-specific
paths: parse the live document before editing, preserve unrelated user fields,
write a private temporary file and atomically replace, commit `current` only
after the live configuration succeeds, and restore the exact captured bundle
when a later commit fails. OpenCode's additive provider model must not be
mistaken for the switch-only model used by Codex or Claude Code.

## Goal

Allow an authenticated Controller user to configure and switch DSH model
providers, select a DSH provider/model/reasoning effort for the current Agent
Session, and recover the same Session after missing credentials or a removed
route. At the same time, establish one narrow Runtime configuration and Session
model interface that later adapters can implement without teaching the Web UI
their configuration-file formats.

## Architecture Decision

### 1. Runtime identity stays separate from model-provider identity

`agent_sessions.provider=dsh` continues to mean the Runtime Adapter. Values
such as `deepseek-official`, `anthropic` or `acme-gateway` are DSH-internal LLM
routes and must not replace that persisted Runtime binding. Existing DSH,
OpenCode, direct DeepSeek and Fake Sessions therefore retain their current
adapter identity and recovery behavior.

### 2. One common configuration surface, native implementation per Runtime

The Agent domain exposes optional capabilities over the existing Adapter
registry:

- a Runtime configuration capability lists redacted provider profiles and
  performs revision-checked configure/remove operations;
- a Session model capability prepares the Runtime Session, lists its current
  selection and grouped catalog, and validates/selects a complete
  provider/model/optional-reasoning value.

The HTTP API addresses these capabilities by Runtime and product Session,
never by a Browser-supplied host path. DSH implements them with native Host RPC.
Future OpenCode/Codex/Claude implementations may use stable native protocols or
managed configuration files behind the same capability, but no unused generic
file writer is introduced in this task.

### 3. DSH remains the configuration fact source

AISummoner does not copy DSH provider profiles, model catalogs or credentials
into SQLite. It persists only the already-existing opaque external DSH Session
ID. Opening the model selector may lazily create that private DSH Session so
`session.models/selectModel` remain authoritative. The Service serializes this
preparation/selection against Turn admission; a Turn either sees the completed
selection or returns `TURN_IN_PROGRESS` without a half-applied product state.

Provider configuration joins DSH's directory, redacted settings layers and
value-free credential descriptions. Only curated cross-provider fields leave
the Server: route/display name, active/configured/custom/removable flags,
Base URL, API protocol, bounded model metadata, namespace revision and
configured/writable credential status. Credential references, sources,
values, settings schemas, unknown profile fields and raw DSH errors remain
private.

### 4. Preserve DSH's selection lifetime

`session.selectModel` applies to the current DSH Session's next assembled step
and saves the same selection as DSH's default. A running step retains the
selection it already assembled. DSH records the actual provider/model in its
request header; AISummoner does not invent a second model-state store.

The composer receives the current selection from `session.models`, groups
models by Provider, and exposes only the reasoning efforts advertised for the
exact model. Settings manages Provider profiles and credentials. These are two
different lifetimes and are not merged into one ambiguous global dropdown.

### 5. Future configuration-file adapters follow a managed-document contract

The next adapters must resolve a fixed, Server-owned configuration root; reject
symlink/path escape; read with a byte limit; parse the complete existing JSON,
JSONC or TOML document; apply only adapter-owned keys while retaining unrelated
content; validate the candidate; write mode-0600 credential-bearing files by
same-directory temporary file plus atomic rename; and publish the selected
profile only after every required file succeeds. Multi-file writes require an
exact snapshot and bounded rollback. A malformed live file is an actionable
error, never permission to replace it with an empty document.

OpenCode will use additive provider entries when its rich adapter work begins;
Codex and Claude Code may use switch-oriented profiles. This task documents the
distinction but does not touch their files.

## Public API Shape

- `GET /api/v1/agent-runtimes/{runtime}/providers`
  returns a no-store, redacted provider directory, supported custom-route
  protocols and the revision used to create a new custom route.
- `PUT /api/v1/agent-runtimes/{runtime}/providers/{provider}`
  applies one bounded curated profile with `expected_revision` and an optional
  write-only `api_key`. Existing unexposed DSH fields survive through minimal
  path operations.
- `DELETE /api/v1/agent-runtimes/{runtime}/providers/{provider}`
  removes only a user-owned removable profile and its conventionally owned
  credential; composition providers and unknown credential references remain.
- `GET /api/v1/agent-sessions/{session}/models`
  returns the selected provider/model/effort, routability, grouped catalog,
  provider-local catalog failures and value-free current credential state.
- `PATCH /api/v1/agent-sessions/{session}/models`
  validates and selects a complete advertised provider/model/optional effort
  only while the product Session is idle or failed.

All writes require the existing exact Origin and authenticated owner. IDs,
strings, model counts, JSON bodies and DSH replies remain bounded. Responses
never echo an API key, credential reference/source, raw settings document or
provider diagnostic containing private details.

## Required Behavior

### Provider management

- Show configured DSH providers as rows and dormant catalog providers through
  an Add flow; allow a validated custom route with Base URL, supported protocol
  and at least one model.
- Official DeepSeek remains a composition route and cannot be deleted. A
  user-materialized pi-ai profile can be removed only when DSH reports that no
  composition base owns it.
- Saving a key derives or reuses the private DSH credential reference, writes
  it only through `credentials.set`, clears the Browser field and refreshes
  value-free status. Empty key input preserves the stored key.
- Use DSH namespace revisions so a stale dialog cannot overwrite another tab
  or an external settings edit. A settings success followed by credential
  failure is reported as a partial, reloadable result rather than falsely
  claiming nothing changed.
- Base URLs accept HTTPS, plus explicit literal-loopback HTTP for a model
  service deliberately colocated with the Server; credentials are never
  redirected by AISummoner, and the pinned DSH provider transport retains its
  own no-credential-forwarding contract.

### Model selection and Turn preflight

- The composer shows the actual DSH model, not only the `DSH` Runtime label.
- Model options are grouped by Provider; only advertised reasoning efforts are
  selectable. Choosing one updates the same Session and does not create a new
  product conversation.
- Model preparation/selection is serialized with Turn admission. Selection is
  disabled while running/waiting, and stale responses cannot replace a newer
  Session's state.
- Preflight checks the current selected route, not hard-coded
  `DEEPSEEK_API_KEY`. A named missing credential or unroutable provider fails
  before the user message is persisted and opens Provider Settings; a route
  with provider-native authentication is allowed to prove itself at request
  time.
- Existing and failed Sessions remain recoverable after configuring a key,
  re-adding a route or choosing another model.

### Controller interaction

- Replace the single DeepSeek-key block in `Agent 与模型` with a DSH-aligned
  Provider list/editor and Add flow; retain the existing Runtime roadmap and
  default Remote permission controls.
- Put the model selector in the Agent composer. Settings remains reachable from
  both Device Hub and Workspace.
- Keep all product copy Chinese and retain the accepted DSH light visual
  system. No iframe, DSH login or Browser-direct private Host endpoint is added.

## Relevant Files

- `internal/agent/types.go`, `internal/agent/service.go`
- `internal/dsh/adapter.go`
- `internal/agentapi/`
- `cmd/aisummoner-server/server.go`
- `web/src/api/`
- `web/src/agent/`
- `web/src/components/ControllerSettingsDialog.tsx`
- `web/src/pages/AgentPage.tsx`
- `web/src/styles.css`
- `docs/baseline/`, `docs/decisions/`, `docs/agent_context/`

## Verification

All heavy work remains serial and low-memory.

```bash
GOMAXPROCS=2 go test -count=1 -p 1 ./internal/dsh ./internal/agent ./internal/agentapi ./cmd/aisummoner-server
GOMAXPROCS=2 go test -race -count=1 -p 1 ./internal/agent ./internal/agentapi
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run --maxWorkers=1
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Focused regressions must prove DSH RPC decoding/bounds, redacted provider joins,
revision conflict, custom-route validation, unknown-field preservation,
credential non-disclosure and partial failure; owner/runtime capability checks;
model preparation/Turn-selection races; current-provider credential preflight;
composer selection and Provider Settings recovery. The final candidate also
receives full serial Go test/vet/build and Web test/build gates.

The bounded ASD deployment may replace only the existing AISummoner test
service and its private DSH child. It must preserve Caddy/nginx and every
unrelated listener/container, record rollback state, and run one real
provider-directory/model-selection/benign Remote Turn smoke without printing a
credential.

## Documentation Requirements

- Add an ADR for the Runtime configuration/model-selection boundary and future
  managed-document transaction rules.
- Update protocol/security and Alpha direction baselines, project context,
  roadmap and public usage documentation.
- Record exact DSH/CC Switch reference commits, source hashes, low-memory
  commands, failures/retries and deployment evidence in `summary.md`.

## Out Of Scope

- OpenCode, Codex or Claude Code provider-file mutation or rich Runtime work.
- OAuth login/account switching, provider failover/load balancing or a local
  translation proxy.
- Arbitrary DSH settings/schema exposure, advanced headers/retry/transport
  fields, automatic provider model polling or credential value reads.
- Standard event v2, cancel/steer/queue and unrelated Controller/Remote Client
  refactors.

## Acceptance Criteria

Task022 is ready for review when a Controller user can add/configure a non-
official DSH provider, see its models, select a provider/model in the current
Session, run a Turn through the unchanged AISummoner Remote approval/SSH path,
switch back without creating a new conversation, and recover cleanly from a
missing key or stale configuration; all secrets and unrelated DSH settings
remain private, and no non-DSH Agent behavior changes.
