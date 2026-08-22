---
task_id: task012
type: plan
status: implemented
from: orchestrator
to: implementation
revision: 0
requires_review: true
---

# Task 012 Plan: Native Agent Timeline And Real OpenCode Test Deployment

## Goal

Replace the misleading Fake-style Agent experience with a provider-neutral,
native conversation flow, then switch the bounded Task011 test deployment to
the real loopback OpenCode adapter without disturbing existing ASD services.

## User Direction

- Use `/home/myself/workspace/deepseek-harness` as the primary interaction
  reference.
- Do not embed or maintain DSH as AISummoner's Agent implementation.
- Preserve a unified invocation and presentation adapter boundary so future
  Agent runtimes do not require separate duplicated pages.

## Implementation Scope

1. Represent user messages, assistant text and tool calls in one append-ordered
   Web timeline; later lifecycle events update their original nodes in place.
2. Move pending tool approval into the composer area and keep timeline tool
   records compact and expandable.
3. Add provider and tool presentation adapters with safe unknown fallbacks.
4. Label Fake honestly as a deterministic test adapter that does not understand
   natural-language requests.
5. Preserve normalized Go domain events and include tool metadata required by
   the presentation layer; Browser must not parse provider-native wire events.
6. Document the architecture in ADR-0003 and keep owner checks, approval,
   output/time limits, SSH execution and loopback-only OpenCode unchanged.

## Deployment Scope

- Reuse only the scoped Task011 Server/database/Caddy deployment.
- Start OpenCode 1.18.11 as a separate non-root, read-only-rootfs container that
  listens only on ASD `127.0.0.1:14096` and shares only the private workspace
  path required by the Server.
- Start the Server's bridge only on `127.0.0.1:14097`.
- Keep nginx, TCP 80, Caddy TCP 10001, unrelated containers and the Remote
  Client unchanged.
- Build a new Server binary with the reviewed Web dist, keep exact binary/env
  rollback copies, and fail back to the current Fake Server if startup health
  or real adapter verification fails.
- Never print OpenCode Basic Auth, bridge secret, web credentials, cookies or
  provider response bodies containing sensitive data.

## Verification

- Focused and full Web tests; production Web build and non-placeholder dist.
- Relevant Go tests plus a static Server build embedding that exact dist.
- Authenticated OpenCode health/version and model inventory without exposing
  credentials.
- New real Agent Session reports provider `opencode`, emits ordered assistant
  and tool events, executes `remote_exec` on the bound Remote after approval,
  and finishes with a genuine model response.
- If the free endpoint is rate-limited or unavailable, record that exact
  classification and do not silently use Fake.
- Post-deploy checks prove Server/Caddy/nginx/unrelated containers are still
  healthy and only 14096/14097 were added as loopback listeners.

## Out Of Scope

- Importing DSH/Cordis or running provider code in the Browser.
- A second real Provider in this task.
- Terminal reconnect, session replay, model reasoning display, attachments,
  queues or cancel UX beyond the current domain contract.
- Changing public TLS, pairing, Device identity, Tunnel, SSH or authorization
  rules.
