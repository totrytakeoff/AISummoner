---
task_id: task005
type: review
status: approved
from: reviewer
to: orchestrator
revision: 1
decision: APPROVED
next_action: next_task
---

# Task 005 Review

## Decision

APPROVED

## Findings

No blocking issues found.

Revision 0 requested three fixes; revision 1 closes each one:

1. **Raw-socket force close is real and joined.** `acceptWebSocket` captures the exact `net.Conn` returned by the successful HTTP hijack, and `networkWebSocket.ForceClose` closes that connection directly. Cleanup still starts exactly one static coder/websocket close handshake, waits the configured grace, then cancels workers, closes the PTY, force-closes the raw socket when necessary, and joins the data workers plus close worker before returning and releasing capacity. The two manual-TCP tests stop after reading `101`, never read or answer the close frame, and prove opener failure and `CancelDevice` complete near the injected grace and below one second. These regressions would fail under the revision-0 `Close`/`CloseNow` behavior.

2. **Completion is published before registry removal.** `release` now closes `session.done` under `Handler.mu` before deleting the session and user count. A lifecycle caller therefore cannot observe the session absent before completion has been published. The deterministic `afterFinish` barrier exercises both `CancelDevice` and `Close` at this exact boundary without relying on sleeps.

3. **Predictable pre-101 failures use the API envelope.** The handler mirrors coder/websocket's predictable request checks before admission: HTTP version, Upgrade/Connection tokens, version, single valid 16-byte key, Origin/Host consistency, and Hijacker availability through bounded `Unwrap` traversal. Real HTTP regressions assert JSON content type, matching response/body request IDs, no reflected sentinel, zero PTY opens, and zero admission residue; a real upgrade through an `Unwrap` wrapper also succeeds.

The prior revision-0 findings are retained in the review history above and are resolved by this revision. The remaining reviewed behavior continues to match the frozen Task005 boundaries: Origin precedes authentication, owner/online checks precede admission, generation invalidation closes the unpair race, terminal payloads and resize controls remain bounded, and post-upgrade reasons/logs remain static and non-secret.

Residual integration constraint: Task008 wrappers must preserve `http.Hijacker` directly or expose `Unwrap() http.ResponseWriter`, and must invoke the joined `CancelDevice`/`Close` lifecycle methods. Unexpected failures during the destructive Hijack after a valid preflight may already have committed `101`; correctly, those cannot be converted back into an HTTP JSON response.

## Reviewer Verification

- Method: independent read-only review of the frozen Task005 plan and revision-1 summary, the complete `internal/terminal` implementation and tests, the relevant baseline/ADR constraints, and coder/websocket's accept/close semantics. Result: all three revision-0 required fixes are present in production paths and have suitable regression oracles.
- Commands: not rerun by the reviewer. The frozen author evidence records focused package PASS, `-count=10` PASS, race PASS, and both command builds PASS under the approved ASD resource gate; the reviewer did not duplicate those heavy runs.
- Reviewed source SHA-256: `handler.go` `ca6b76691ed0273e5bf37fac3bf533a8a9e76a5601816b4e97580ffd21252304`; `session.go` `92320bdf5a5b1e4b6cbd96fec24cee04f08e321c91ca330689012928672a5070`; `handler_test.go` `f6cad491eac8ec632c434f2faf57e4da69932756d0eb74b369c1227232a72e93`; `websocket_test.go` `a724289341c269f4cf958370dfd691c779a9a2183593f2c63a2cb5248fbd7c8a`; `summary.md` `e07788a14e8cee80de3f19ad5adcab823ae34be622315498ec43a8980df751db`.

## Next Action

- Task005 may advance to Task008 integration and full-stack acceptance.
