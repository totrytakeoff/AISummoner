---
task_id: task019
type: plan
status: completed
from: planner
to: coder
revision: 0
requires_review: true
---

# Task 019 Plan: Controller Component Launcher And Chinese UI

## Goal

Close the two human findings on the accepted DSH-first visual baseline before
starting the real DSH Runtime integration: remove duplicate Device/Terminal
entry points and present the complete Controller user interface in Chinese.

## Required Changes

1. Replace the separate Workspace toolbar Device and Terminal buttons with one
   component-dock toggle. Opening it restores the last selected dock tab;
   Terminal and Device are selected only inside the dock.
2. Preserve deep-link `?dock=terminal`, Session/Device disposal, maximization,
   narrow-screen visibility and focus restoration.
3. Translate all user-visible Controller static copy, accessible names, status
   text, fallback errors and confirmations into Simplified Chinese. Product and
   protocol names such as AISummoner, Agent, DSH, DeepSeek, OpenCode, Codex,
   Claude Code, Terminal, Device ID and command output remain recognizable
   where translation would obscure the name.
4. Localize standard Server error codes at the Browser boundary without
   exposing raw unknown details; retain status, code and request ID.
5. Update focused tests to assert the Chinese interaction contract and add a
   regression proving the toolbar exposes only one component launcher while
   the dock owns both tabs.

## Verification

- Full Vitest suite.
- TypeScript and Vite production build.
- `git diff --check`.
- Playwright visual smoke at 1440×960 and 1024×768, including an open dock.

## Out Of Scope

- DSH Runtime protocol, Server-side Runtime supervision, deployment, Go data
  plane, Terminal protocol, and Remote Qt Client changes.

## Acceptance

Task019 is complete when the Workspace has one unambiguous component entry,
the dock itself switches Terminal/Device, and no normal Controller surface
shows leftover English UI copy except proper names or remote/model content.
