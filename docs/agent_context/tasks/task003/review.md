---
task_id: task003
type: review
status: approved
from: reviewer
to: orchestrator
revision: 4
decision: APPROVED
next_action: next_task
---

# Task 003 Review — Revision 4

## Decision

APPROVED

## Findings

No blocking issues remain in the bounded revision-4 scope.

The revision replaces the rejected pre-call scheduling signal with a positive
close-contender admission boundary. With the test hook installed,
`lockTerminalForClose` first calls `terminalMu.TryLock`; the hook fires only
after that real acquisition attempt fails while the resize file operation still
owns the mutex, and immediately before the close path performs the blocking
`Lock`. Waiting for `closeAdmitted` therefore establishes that production
`closeTerminal` has reached and contended on the exact lifecycle boundary under
test. The file-close and termination-completion channels remain blocked until
the resize barrier is released, after which resize, close and termination are
all joined and their operation counts and terminal state are checked.

The oracle is mutation-sensitive for the required boundary: bypassing the
close-side helper removes the admission signal, while allowing the close path
to cross the resize-held boundary publishes the file-close/termination signals
before release. It no longer treats merely starting a goroutine as evidence of
lock contention.

Production behavior is unchanged. `terminalCloseContended` is package-private,
is never populated by either process constructor, and is read only after object
publication. Its nil path executes the same single blocking
`terminalMu.Lock()` used by revision 3; it does not use `TryLock` or invoke a
callback. The injected test object initializes the hook before starting either
goroutine and never mutates it, so the seam adds no new shared write or race.
The existing `terminalMu -> lifecycleMu` ordering and all revision-2 pidfd,
descendant cleanup, reap and pump-join behavior remain untouched.

## Reviewer Verification

- Read the Codgent reviewer workflow, repository instructions, Task003
  revision-4 plan and summary, revision-3 review, and the complete bounded
  source/test change in `internal/sshserver/server.go` and `server_test.go`.
  Result: **PASS** for the sole requested deterministic admission fix,
  production nil-path equivalence and race safety.
- Author evidence: twenty focused PTY resize iterations, focused SSH Server
  race, exact development WS/yamux/strict SSH composition race, vet and both
  command builds on the formatted hash-matched tree.
  Result: **RECORDED PASS**.
- Independent ASD-Host rerun: not performed. The revision is a narrow
  package-private test seam, source evidence directly establishes the fixed
  oracle, and the author already reran both relevant race targets.

## Next Action

- Task003 revision 4 is approved. Continue integration verification; production
  TLS/WSS and three-host process acceptance remain Task010 scope.
