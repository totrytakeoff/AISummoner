---
type: todo
status: active
updated_by: task014_implementation
---

# Todo

## Current

- [x] task001 foundation implemented and independently approved (revision 2).
- [x] task002 Tunnel/Remote identity implemented and independently approved (revision 2).
- [x] task003 revision 4 independently approved after fixing and deterministically covering PTY resize concurrent with PTY close.
- [x] task004 WebUI revision 1 independently approved; real-browser integration remains in task010.
- [x] task005 Terminal gateway independently approved (revision 1).
- [x] task006 Agent domain/Fake Adapter independently approved (revision 2).
- [x] task007 OpenCode integration independently approved (revision 0).
- [x] task008 full Server composition, lifecycle, static Web and deployment assets independently approved (revision 0); the Server image build retains an explicitly recorded ASD `proxy.golang.org` timeout for task010.
- [x] task009 revision 2 independently approved after trusted proxy provenance and complete merged verification.

## Next

- [x] task010 independent acceptance review and exact scoped cleanup. The review is `BLOCKED` only on external public TCP 443 ingress; functional evidence remains frozen in `docs/acceptance/mvp-0-2026-08-13.md` and cleanup evidence in its companion record.
- [x] task010 revision 2 public rerun on user-authorized TCP 10001. Bounded probes proved 10001 and 10002 free and reachable from lzr; 10001 was selected and 10002 remained unused. Fresh strict production Client WSS and Browser HTTPS/Secure-cookie/Terminal-WSS/Agent-SSE passed; evidence is frozen for independent review.
- [x] task010 revision 2 independently approved. Exact Client/Server/Caddy shutdown and secret/test-artifact cleanup completed; SQLite, Device identity, binaries and non-secret logs retained for audit/recovery.
- [x] task011 deploy a fresh bounded Fake Server on ASD, build a verified Linux
  x86_64 Client AppImage, and leave the local non-root Client connected for
  user testing.
- [x] task012 implementation ready for independent review: DSH-inspired,
  provider-neutral ordered Agent timeline; composer approval takeover;
  provider/tool presentation adapters; real loopback OpenCode 1.18.11; and
  three live repaired full-access/per-command Turn proofs with exact rollback.
- [x] task013 ready for independent review: correct OpenCode final/error ordering, normalize and
  separate reasoning, resume the latest owned Agent Session on page entry, and
  adapt DSH's conversation/session interaction model without importing its
  privileged backend or bypassing AISummoner approval/SSH boundaries.
- [x] task014 revision 2 ready for independent review: direct bounded
  DeepSeek thinking/tool streaming; AISummoner-owned history/approval/SSH;
  automatic default conversation; separate Think/tool/final rows; explicit
  session-elevation confirmation; plus an authenticated Web form whose key is
  held only in Server memory and starts a new DeepSeek conversation. The new
  Server/WebUI is deployed on the preserved ASD TCP10001 runtime with an exact
  old-binary rollback. Revision 2 removes the obsolete cumulative per-Turn
  tool-call count wall while retaining all time, approval, byte, protocol and
  lifecycle bounds; focused/repeat/race/full/vet/build and safe live deployment
  passed.
- [x] Task014 human Provider proof: the user confirmed the direct DeepSeek
  Agent and Terminal reached the real Remote through the complete production
  chain. The only observed stop was the former local twelve-call wall, now
  removed and deployed; the user ended further MVP testing.
- [ ] Human test: import the Task011 public test CA on the controller device,
  log in, claim the current local pairing code, and exercise Terminal/Agent.
- [ ] Human Ubuntu test: verify `ubuntu-client/SHA256SUMS`, run the AppImage as
  a non-root user with the scoped CA, and claim the new Ubuntu Device.

## Blocked

- The former TCP 443 blocker is superseded by explicit human authorization to
  use an already-open unused port. Revision 2 is active on TCP 10001; no cloud
  firewall mutation is required. The revision-0 review and 443 runbook remain
  immutable historical records and must not be presented as the current plan.
