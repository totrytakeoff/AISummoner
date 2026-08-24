---
task_id: task018
type: plan
status: ready_for_review
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 018 Plan: DSH-first Controller Rebaseline

## Goal

Replace the remaining MVP/dashboard presentation with a source-aligned DSH
Controller experience while retaining Task017's proven Device-scoped transport
and ownership shell. Retire the old Manage page, make Settings part of the
Workspace, and make DSH the explicit first-class experience adapter without
claiming that its Runtime backend is already integrated.

## Inputs

- ADR-0005 and baseline 04.
- Task017 revision 2 approved Workspace foundation.
- DSH MIT checkout `47f943859bef60e4160492346772ded9b24f765a`.
- Existing AISummoner Agent snapshot/SSE, tool approval and Terminal protocols.

## Required Controller Changes

1. Make the authenticated Workspace a true full-viewport DSH shell rather than
   nesting it below the legacy global top bar.
2. Keep Task017's resizable three-zone foundation, but align its rail, rows,
   conversation axis, messages, reasoning, tools, approval and composer with
   DSH geometry and light design tokens.
3. Replace text glyph controls with a small accessible SVG icon set.
4. Remove the old Device Manage surface. `/devices/{id}` migrates to
   `/devices/{id}/workspace?settings=device`.
5. Add one DSH-style Settings modal with General, Agent & Models and Device
   sections. Move DeepSeek key configuration and unpair into that modal.
6. Add an explicit DSH experience descriptor/capability seam. Runtime labels
   continue to reflect the actual bound provider.
7. Rebaseline Login and Device Hub to the same calm neutral system so the
   product no longer changes visual language before entering the Workspace.
8. Preserve Session/Device transport disposal, owner isolation, approval
   semantics, narrow fallback, keyboard resizing and reduced motion.

## Tests

- Old Device/Agent/Terminal URLs migrate to the correct Workspace state.
- No visible Manage action or old Device page remains.
- Settings opens from the DSH rail, switches sections, configures DeepSeek
  without Browser persistence, displays Device metadata and performs confirmed
  unpair.
- DSH experience capability descriptor is provider-independent while runtime
  labels stay truthful.
- Existing Task017 layout/session/transport tests, Agent tests, Login and Device
  Hub tests remain green.
- Full Web test, TypeScript/Vite build, `git diff --check`, and a local browser
  visual smoke at desktop and narrow widths.

## Out Of Scope

- DSH Runtime/Host integration, event-v2 Server schema, provider credential
  persistence, cancel/steer/queue/retry/rename/archive/fork APIs, Markdown/diff
  libraries, arbitrary docking and desktop streaming.
- Go data-plane, Remote Qt Client, deployment and TLS changes.

## Acceptance

Task018 is complete when the product visibly and behaviorally reads as a DSH
Controller from login through active Agent work, Device management exists only
inside Workspace Settings, and every exposed operation is backed by an existing
AISummoner authority or clearly labelled as a future capability.
