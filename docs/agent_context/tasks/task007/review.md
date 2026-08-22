---
task_id: task007
type: review
status: approved
from: reviewer
to: orchestrator
revision: 0
decision: APPROVED
next_action: next_task
---

# Task 007 Review

## Decision

APPROVED

## Findings

No blocking issues were found in the frozen Task007 scope.

1. **Current-Turn SSE correlation and optional `textID` fallback are fail-closed.** The Adapter subscribes before dispatch but arms projection immediately before the prompt request, generates a fresh `msg_*`, and accepts text only after an assistant identity has the exact current prompt as `parentID`. A matching user echo may authorize a provider error but cannot authorize idle success; old assistant/tool/status activity cannot do so either. Stable parts and compatibility deltas latch per part while `textID` is present. The first missing-ID compatibility event freezes the whole-message fallback to the chronologically first actually emitted family, after which presence changes are tolerated without cross-family duplication. Empty/equal/stale full snapshots do not select a family, divergent rewrites fail closed, event identities/counts and the shared pending record/byte pool are bounded. At the reviewed snapshot `messageUpdated` calls `clearPending` exactly once; its full-versus-delta accounting does not underflow.

2. **Adapter lifecycle preserves fatal callback errors.** Session creation and async prompt use their distinct verified DTO fields, every request carries Basic Auth, redirects are rejected, every bounded response body is closed, and status 204 only completes dispatch—not the Turn. Mapping deactivation joins callbacks before the SSE parser is canceled/closed and joined. `waitForTurn` and the post-join failure drain preserve the original `RemoteExecInvoker` error even when provider idle or Turn cancellation wins the initial select; failed/canceled work then receives an independent bounded abort.

3. **The bridge is session-bound, bounded and joined.** It accepts only loopback POST requests, validates the exact timestamped 32-byte HMAC proof with constant-time comparison, resolves only an active external Session's injected invoker, and never accepts user/device selectors. `beginCall` performs WaitGroup registration under the same activity lock that closes admission, preventing Add/Wait races. The capacity-one gate covers the complete callback and wakes on mapping/request cancellation. Deactivation cancels and joins executing and queued callbacks; Bridge shutdown rejects new activations and joins existing mappings. Body/response/callback limits are explicit, non-nil invocation and response-delivery errors become Adapter-fatal, and errors/logs do not contain credentials, commands or output. Real TCP tests exercise both body-read and slow-reader write deadlines instead of relying only on `httptest.ResponseRecorder` deadline behavior.

4. **Workspace and tool isolation implement both deny layers.** Each product Session maps to a stable opaque SHA-256/base32 directory. Root/workspace/subdirectories require real 0700 directories; files require real 0600 regular files; symlink components, missing/changed files and every extra entry are rejected. Reuse verifies exact embedded bytes and never repairs an altered workspace. Root and embedded policy/tool assets are byte-identical. Project policy is exactly wildcard deny plus `remote_exec` allow; every frozen built-in is independently false in each prompt. The TypeScript tool has no process/filesystem API, validates one fixed loopback callback URL, rejects redirects, forwards `context.abort`, signs only the external Session proof, and sends no host/user/device selector.

5. **Scope and verification evidence are adequate.** The implementation stays within the Task007 packages/assets and the already-authorized `.env.example` documentation. Deterministic tests cover DTOs, dispatch/body-close barrier, reused-session correlation, both text families, monotonic snapshots, bounds, fatal races, HMAC/cross-session isolation, serialization/join, real TCP deadlines, redaction, exact workspaces and static tool capabilities. The author's fresh isolated ASD evidence records focused PASS, selected high-risk tests at `-count=10` PASS, race PASS, vet PASS and both command builds PASS.

### Residual Risks

- No live free-model inference was claimed or required for this deterministic gate. OpenCode quota/authentication, the ASD sidecar version upgrade, and a three-host smoke remain Task008/010 integration work.
- Task008 must mount the Bridge on a separate direct loopback HTTP server with bounded server header/shutdown settings and deadline-capable response writers, generate a strong process-local secret, and keep OpenCode plus the Bridge in the same loopback namespace. Exposing either listener publicly would invalidate the reviewed boundary.
- Provider `session.idle`/`session.error` has no Turn identifier. The implementation therefore intentionally requires current prompt/assistant evidence and ordered SSE delivery; a provider error emitted before either identity is conservatively ignored and will be resolved by later correlated evidence or the bounded Turn timeout.

## Reviewer Verification

- Review snapshot: `2026-08-13 05:26:05 +0800`; plan SHA-256 `3b1293a75ce17831b3be9bd18d620c4af001b334adf34e645a80c7d5db6f1553`; summary SHA-256 `ea9b5404d52244699412c6e0a80599a5922926d752841128a1c41d2c35fffaa0`.
- Independently inspected all Task007 production/test files and both root/embedded assets. Production hashes: `adapter.go` `f9c5299d554b778bfad327cc3a83a953b1f4c1449b695feec6d021d8b0935684`; `sse.go` `bdca82adc74faaf07584e831a868e5ddb4ed86e73e42d88ad34b003f570d48fc`; `workspace.go` `9716736500947a390dbd647ee9b7219e64eed115a5e35ce1b081ec10a4510480`; `bridge.go` `54dd1b26ccffd1f5bc916c13508618e335c848d01a9d1283578be4293462758f`.
- Asset equality: root and embedded `opencode.json` both `61f93e1becfe74a108f5288fb687ff9e44a3d1e00b30e1900f56777e3f38253e`; root and embedded `remote_exec.ts` both `2700d7dc630a62398f0cadcc4785bdc0b32fd3561bdd82e652906145b939f75a`.
- Commands: not rerun by this reviewer. Fresh author evidence already covered focused, ten-repeat, race, vet and build gates under the approved ASD resource policy; the independent review concentrated on source-level falsification and test-oracle adequacy.

## Next Action

- Task007 is approved. Task008 may compose the Adapter and loopback Bridge while preserving the residual deployment boundaries above.
