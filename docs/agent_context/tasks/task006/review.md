---
task_id: task006
type: review
status: approved
from: reviewer
to: orchestrator
revision: 2
decision: APPROVED
next_action: next_task
---

# Task 006 Review — Revision 2

## Decision

APPROVED

## Findings

No blocking findings remain in the bounded revision-2 scope.

The per-Turn capacity-one gate now serializes the complete `turnInvoker.Invoke` operation: admission/counting, approval-mode observation and upgrade, durable tool lifecycle, remote execution, and terminal persistence. Waiting callers select on their own context and recheck cancellation immediately after acquisition, so cancellation returns without consuming the tool budget, creating a row, or invoking the executor. `Service.Decide` intentionally remains outside the gate, allowing it to resolve the currently pending invocation without deadlock. Consequently, an `approve_session` decision has one defined order: the first call completes its decision/start/execution/finalization while holding the gate, updates the Turn's in-memory approval mode, and only then can the next queued call observe Full Access.

The three deterministic regressions directly exercise the formerly unsupported public-boundary concurrency:

- more than 12 simultaneous callbacks yield exactly 12 completed durable/executed tool calls and `ErrToolCallLimit` for every excess call, while only one executor call is admitted at a time;
- a callback queued behind an `approve_session` call cannot cross the gate early, then auto-starts without a decision only after the first call has established Full Access;
- a queued callback canceled while the gate is occupied returns promptly with `context.Canceled`, creates no durable/executor work, and leaves all 12 slots available to the admitted plus follow-up calls.

The revision introduces no new public API shape or shared mutable state outside this per-Turn ordering boundary. The six revision-0 fixes accepted in revision 1 remain unchanged.

## Reviewer Verification

- Narrow source review: `turnInvoker` construction and the full `Invoke` body in `internal/agent/service.go`, plus the three new concurrent Adapter regressions in `internal/agent/service_test.go`.
  Result: **PASS**.
- Author evidence: focused Agent tests, ten-repeat concurrent regressions, focused `-race`, vet and both command builds on an isolated ASD-Host copy with bounded concurrency and matching source hashes.
  Result: **RECORDED PASS**.
- Independent remote rerun: not performed. The revision is narrow, the author evidence is fresh, and source plus deterministic tests fully exercise the requested boundary.

## Next Action

Task006 is approved. Task007 may consume its Adapter and `RemoteExecInvoker` contracts; its Adapter/Executor implementations must continue honoring context cancellation as the trusted interface contract requires.
