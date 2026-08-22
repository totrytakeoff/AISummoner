---
type: collaboration_protocol
status: active
updated_by: planner
---

# Agent Collaboration Protocol

## Roles

Planner/Reviewer Agent:

- Owns architecture direction, task decomposition, and quality gates.
- Writes task plans before implementation starts.
- Reviews implementation summaries, diffs, tests, and docs.
- Writes reviews with one decision: `APPROVED`, `CHANGES_REQUESTED`, `BLOCKED`,
  or `NEEDS_HUMAN`.

Implementation Agent:

- Reads shared project context and the current task plan.
- Implements only the requested scope.
- Runs required verification commands when feasible.
- Writes the implementation summary after changes.
- Does not silently expand architecture or change unrelated modules.

## Workflow

1. Planner writes or updates the project context and roadmap.
2. Planner writes the current task plan.
3. Implementation Agent implements the task.
4. Implementation Agent writes the task summary.
5. Reviewer reviews code, docs, and verification output.
6. Reviewer writes a decision.
7. Orchestrator, user, or next agent follows the decision.

## Decisions

- `APPROVED`: move to the next task or final delivery.
- `CHANGES_REQUESTED`: revise the same task.
- `BLOCKED`: pause until the blocker is resolved.
- `NEEDS_HUMAN`: pause for human approval or direction.

## Engineering Rules

- Preserve project architecture unless a plan explicitly changes it.
- Keep task changes narrow.
- Add focused tests for new behavior where practical.
- Do not hide failed verification commands.
- Do not revert unrelated user or agent changes.
- Keep durable docs current when architecture or behavior changes.
