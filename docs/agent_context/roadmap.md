---
type: roadmap
status: active
updated_by: coder
---

# Roadmap

## Completed: MVP-0 Vertical Slice

- [x] task001-task010: foundation, Tunnel/SSH, Terminal/Agent, composition,
  hardening, three-host E2E and independent approval.
- [x] task011: bounded ASD deployment and Linux x86_64 CLI AppImage.
- [x] task012-task014: ordered Agent interaction, direct DeepSeek and removal of
  the cumulative tool-count wall.
- [x] Human proof: Terminal and direct DeepSeek Agent completed the real Remote
  chain; the user declared the MVP loop complete.

## Alpha A0: Direction And Repository Hygiene

- [x] Freeze Alpha product direction and dual-client/runtime ADR.
- [x] Correct root-binary ignore rules so `cmd/` source is visible to Git.
- [x] Human priority amendment: Remote Client first; Qt 6 Widgets selected.
- [x] External no-write Claude UI review distilled into the Qt design spec.

## Alpha A1: Remote Core Daemon And IPC

- [x] task015: reusable Remote state machine around the existing Tunnel/SSHD.
- [x] Same-UID mode-0600 Unix socket with status/events/pause/resume/pairing
  refresh.
- [x] Bounded sanitized local event ring and generic active control-session count.
- [x] Preserve legacy headless `start`; update systemd to daemon mode.

## Alpha A2: Qt Remote Desktop Client

- [x] task016: Qt 6 Widgets status/events/settings application.
- [x] Pairing code/countdown/copy/refresh, connection/controlled status,
  pause/resume and daemon recovery.
- [x] Light/dark/system theme and Apple-like quiet visual system without custom
  window chrome, QML or WebEngine.
- [x] GUI+daemon AppImage, Ubuntu-class build/test and local non-root live
  attach; final independent review and target-host E2E remain release evidence.

## Alpha A3: Controller Workspace Foundation

- [x] task017: Device Hub → Control Workspace navigation.
- [x] task017: Owner-scoped bounded recent Session index.
- [x] task017: Left Session rail, center Agent surface, optional right Terminal/Device
  dock; accessible resize/collapse/maximize and mobile fallback.
- [x] Provider setup entry in the Controller shell with no Browser secret
  persistence; provider profiles/capabilities continue in A4.

## Alpha A4: Agent Domain/UI v2 And DSH

- [x] task018: replace the legacy Controller visual/interaction layer with the
  pinned DSH-first shell, conversation, composer and Settings baseline; retire
  the standalone Device Manage page.
- [x] task019: unify the optional component dock and localize the Controller.
- [x] task020: real DSH Host/Adapter/Capability Bridge → Remote SSH chain.
- [x] task021: Session permission, credential recovery, ordered replay,
  collapse-all, archive/delete and global Settings parity.
- [x] task022: DSH-native multi-provider configuration, redacted credential
  readiness and current-Session provider/model/reasoning selection behind the
  common optional Runtime configuration boundary.
- [ ] Standard event v2, Capability Descriptor and Runtime Session lifecycle.
- [ ] DSH-inspired conversation nodes, tool renderers, planning/question/status,
  cancel/steer/retry/queue and richer Markdown/code/diff UX.
- [x] Pinned DSH adapter with Server-local tools disabled and AISummoner Remote
  capabilities injected.

## Alpha A5-A7: Runtime Adapters

- [ ] A5 OpenCode rich Session/Turn/cancel/usage/permission mapping.
- [ ] A6 Codex App Server over stdio/Unix socket with generated pinned schema.
- [ ] A7 Claude Agent SDK streaming input, permission and resume mapping.
- [ ] Maintain direct DeepSeek as lightweight API Adapter and Fake as
  deterministic test infrastructure.

## Alpha A8 And Later

- [ ] A8.0 task023: Windows Server 2022 CI proves named-pipe/logon-SID carrier,
  DPAPI/ACL, suspended Job, PowerShell, strict ConPTY resize/interrupt/cleanup,
  Qt/MSVC and native packaging contracts. Ordinary-user Windows 11/10,
  wrong-logon, clean-VM and detached production-Core evidence remain; ADR-0007
  stays Proposed.
- [x] A8.1 task024: common Runtime policy, Identity storage, IPC transport and
  SSH execution seams extracted; strict `linux|windows` hello plus Linux
  daemon/SSH/Qt/AppImage gates pass. The user authorized advancing without
  fabricating an independent review.
- [ ] A8.1 task025 (in progress): implement Windows per-user Core, DPAPI
  Identity, private named-pipe IPC and an explicit fail-closed SSH execution
  boundary. Task023 desktop VM evidence remains release debt.
- [ ] A8.2 task026-task027: implement PowerShell exec/Job cleanup and ConPTY
  Terminal through the real WSS/yamux/strict-SSH chain.
- [ ] A8.3 task028-task029: ship a clean-VM Qt engineering ZIP, add the trusted
  Agent Execution Profile/DSH PowerShell path, then sign/package after E2E.
- [ ] Structured Remote file read/search/write/patch/diff.
- [ ] Remote local restrictive permissions.
- [ ] Desktop viewing/input only after a dedicated threat model and ADR.
- [ ] macOS Remote Client only after the Windows platform seam is proven.
- [ ] Multi-user/RBAC, clustering, port forwarding and arbitrary IDE docking
  remain separate future programs.

## Cross-Phase Release Gates

- Preserve TLS, Device identity, owner, SSH verification, approval, limits,
  redaction and joined cleanup.
- Every Runtime is pinned and has fixture/error/cancel/secret tests plus one real
  Remote proof.
- Every client milestone ships rollback, exact hashes and Ubuntu non-root proof.
- Go race, Qt/Node build and Docker work stay serialized behind resource gates.
