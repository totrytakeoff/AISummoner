# Task011 Persistent Test Deployment — 2026-08-21

Run ID: `20260821T090612Z`.

## Outcome

The user-requested testing environment is active:

- ASD serves the reviewed embedded WebUI/Fake-Agent Server at
  `https://122.51.70.33:10001` with a publicly trusted certificate;
- a Linux x86_64 Client AppImage is packaged and verified;
- the local MyArch workstation is connected as a non-root Remote Client and
  has a fresh one-time pairing code in the private handoff directory.

The deployment is for bounded MVP testing, not public production. Browser and
Client connections use the operating system trust store without a private CA.
The scoped Server, Caddy and renewal timer are transient and do not survive an
ASD reboot automatically.

## Public IP Certificate Update

Mobile access exposed that requiring a private CA and the `sslip.io`
compatibility hostname was the wrong browser-facing trust boundary. That
intermediate deployment was rolled back before a replacement was attempted.
Let's Encrypt 2026 production support for short-lived IP-address certificates
was then used to issue an RSA certificate carrying `IP:122.51.70.33`. The
controller URL is now only `https://122.51.70.33:10001`; no browser CA import
is required.

The HTTP-01 path uses an otherwise empty
`/opt/asd-kgrag/frontend/dist/.well-known/acme-challenge/` directory. No nginx
configuration was edited or reloaded; complete pre/post `/etc/nginx` manifests
are byte-identical. The first symlink-based probe correctly failed exact-content
validation because nginx could not traverse the private 0700 home directory;
it was removed and replaced with the scoped directory without weakening any
permissions. A second outside-in sentinel probe passed and the sentinel was
deleted before issuance.

Certbot 5.7.0 was pinned to image digest
`sha256:34ee91d2f43008eb78a007d22f23ed4b2eaa9a454cb27ca2c042b49527a695b4`.
Initial staging issuance, production issuance and renewal simulation all
passed. The first post-issuance `openssl verify fullchain.pem` command failed
because it did not supply the bundled intermediate as `-untrusted`; the
correct `cert.pem + chain.pem + system roots` verification passed before
deployment. The RSA public certificate is valid from 2026-08-21 08:59:11 UTC
through 2026-08-28 00:59:10 UTC. A transient Task011 timer runs the verified
renewal wrapper every 12 hours, with validation and exact certificate rollback
around any Caddy restart.

Only the Task011 Caddy container was restarted. Local and lzr outside-in
requests both returned HTTP 200 with system trust and TLS verification result
zero; an unknown Host returned 421. The old private certificates and Caddy
configuration remain only in scoped rollback directories. No unrelated
listener or container was changed.

## ASD Runtime

- Server binary SHA-256:
  `a736d09017b62f98ed172b88ee1d00e0c76d620ae9857d0c33154036aa8552e8`.
- Server unit: `aisummoner-task011-server-20260821T090612Z.service`.
- Snapshot after the Agent display repair: PID 3262314, uid 1001,
  active/running, zero restarts.
- Server listener: only `127.0.0.1:8088`.
- Agent adapter: `fake`; no OpenCode or Bridge listener.
- Caddy container:
  `aisummoner-task011-caddy-20260821t090612z`, exact ID
  `30fc8ec164a7615df3895ee5a06639dd37412af7b1f2fea73c0aee11803784d4`.
- Caddy image `caddy:2.10.2-alpine`, uid/gid `1001:1001`, host network,
  read-only root filesystem and `restart=no`; it alone listens on TCP 10001.
- Runtime bootstrap password was removed from ASD after the first administrator
  was created. The runtime environment contains no
  `AISUMMONER_ADMIN_PASSWORD`.

Strict TLS checks passed:

- system trust health and WebUI requests returned HTTP 200 locally and from
  lzr, both with verification result zero;
- the public certificate chain verifies through Let's Encrypt `YR2` and has
  an exact IPv4 SAN for `122.51.70.33`;
- RSA-only TLS 1.2 and TLS 1.3 handshakes both passed;
- no private CA is required by the browser or Remote Client.

The Server's exact browser origin was changed from the retired sslip hostname
to `https://122.51.70.33:10001`, then the same scoped Server unit alone was
restarted. A real login through `/api/v1/auth/login` returned 200, issued a
Secure, HttpOnly, SameSite=Strict session cookie, and authenticated
`/api/v1/devices` returned 200. The same login payload with a wrong Origin
returned 403. No credential, cookie value or response body containing private
data was printed during these checks.

ASD nginx remained active and its complete configuration manifest matched the
pre-ACME snapshot. The four existing
containers (`asd-kgrag-qa`, `asd-kgrag-qdrant`, `asd-kgrag-neo4j`,
`mychat-postgres`) remained running. TCP 80 continues serving the pre-existing
application and additionally serves only transient ACME challenge files from
the scoped empty directory. ASD 10002, 14096 and 14097 remained unused by this
deployment. MemAvailable remained about 5.47 GiB with about 2.05 GiB
SwapFree.

## Full-Access Agent Command Display Repair

Manual testing found that Terminal worked while Full Access Agent tool cards
remained at `(command unavailable)`. A read-only SQLite inspection printed no
command or output values and showed that the existing Full Access Session was
idle and all eight tool calls had actually completed with exit code zero,
valid persisted arguments and non-empty output. The execution chain was
healthy; the defect was limited to WebUI event projection.

Full Access does not emit `tool_call.pending`. The original
`tool_call.started` payload contained only a tool ID, and the WebUI therefore
had no command metadata with which to create its first card. The repair makes
the started event self-contained by including the already validated tool name
and arguments, and makes the WebUI project those optional fields without
overwriting metadata from the per-command path.

Focused and deployment evidence:

- the Web event projection test now covers a Full Access started event with no
  preceding pending event; focused 4/4 and full Web 23/23 tests passed;
- TypeScript checking and the production Vite build passed, producing
  `index-BCIzY-1u.js` and a non-placeholder index;
- the new Go event test passed once and 20 times, the full Agent package
  passed, and the Agent race test passed with explicit exit code zero;
- the root Server composition test passed against the tracked placeholder,
  then the verified production Web assets were injected for vet and a static
  `CGO_ENABLED=0` Server build;
- the old Server binary was retained under
  `/home/myself/.local/state/aisummoner-task011/20260821T090612Z/rollback/agent-fullaccess-ui.TFcmzJ/`
  before the same scoped unit was restarted;
- a live public Full Access Session then emitted two started events, both with
  command metadata, followed by two completed events with exit code zero, one
  completed Turn and an idle Session snapshot. The public index referenced
  the newly built hashed JavaScript asset. No command, output, credential or
  cookie value was printed by the oracle.

## Client AppImage

- Artifact:
  `dist/task011/20260821T090612Z/AISummoner-Client-0.1.0-x86_64.AppImage`.
- AppImage SHA-256:
  `6405caf0dbd129aa4dc11dc165758c9bdd51aa1aaacd9a6a149185fb658aa32a`.
- Embedded reviewed Client SHA-256:
  `03bab20d11dfa47d4341cd5ef8da9b9d11bd76ea6daf4938c08859d06ca2d747`.
- Official AppImage appimagetool 1.9.1 SHA-256:
  `ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0`.
- Type-2 runtime SHA-256:
  `389bc941bd9f2d19e35818403872148dcd7d740e89e120a061a7f125b15981d5`.

The AppImage extracted successfully, its embedded Client compared byte for
byte with the reviewed binary, and the CLI usage boundary passed. In a
network-disabled Ubuntu 24.04 container it ran as uid 1000 and returned the
expected CLI usage result. A separate root invocation was rejected before any
network or identity activity. The AppImage is unsigned; users must verify the
recorded SHA-256.

## Local Remote Client

- Unit:
  `aisummoner-task011-local-client-public-20260821T1021Z.service`.
- Runs under local uid 1000 through the AppImage.
- Uses `https://122.51.70.33:10001` and the operating system trust store; its
  unit contains no `SSL_CERT_FILE` override.
- The actual Client child executable hash is the reviewed
  `03bab20d...2d747` binary.
- It owns zero listening sockets and has an established outbound connection to
  ASD TCP 10001.
- `device.json` and `device_ed25519` are mode 0600.
- A pairing offer was received into a mode-0600 file; its value is deliberately
  absent from this record.
- Client and Server logs were checked against pairing/bootstrap/runtime secret
  values without printing them; all scoped redaction checks passed.

## Private Handoff

The local mode-0700 handoff directory is:

`/home/myself/workspace/AISummoner/dist/task011/20260821T090612Z`

It contains separate mode-0600 files for the URL, username, password and
current local pairing code. `ubuntu-client/` contains only the AppImage,
checksum and instructions; it contains no administrator secret, Device key,
CA material or Server key. Retired private test-CA material is retained only
inside clearly named mode-0700 rollback directories and is not part of the
browser or Ubuntu Client workflow.

## Retained Intermediate Evidence

- The obsolete AppImageKit v13 asset name returned HTTP 404; no file was
  created. Official metadata identified the maintained appimagetool repository
  and version 1.9.1.
- The initial AppImage build passed. A metadata-only rebuild then failed before
  packaging when the tool could not redownload its runtime. The already
  successful AppImage was used to extract and hash the official runtime, and
  the final rebuild passed using that fixed local runtime.
- The first extraction oracle incorrectly expected the root desktop file to
  remain a symlink; appimagetool legitimately materialized it as a regular
  file. The corrected oracle checks executable AppRun, metadata, embedded
  binary equality and CLI behavior.
- The first local process check inspected systemd's AppImage runtime MainPID.
  The corrected cgroup-owned child check located and hashed the actual Client
  process and proved it owns no listener.
- The local workstation does not have Go installed, so the first local gofmt
  attempt could not run. The two Go files were subsequently checked with the
  ASD Go 1.24.4 gofmt, which reported an empty diff before testing.
- One Agent race tool session reported completion before its remote process
  had fully drained. The evidence was not accepted; the single mechanical
  replay recorded an explicit zero exit code and package PASS in the isolated
  staging. The overlap was observed, both processes joined, and no Go process
  remained before build/deployment.
- The first root Server test was run after production Web assets had already
  replaced the tracked placeholder. Its deliberate clean-checkout placeholder
  assertion failed. Restoring the exact tracked placeholder made the root
  composition test pass; verified production assets were injected again only
  for the final build. A first placeholder precheck also used the wrong text
  sentinel and stopped before Go started.
- The first candidate Server build inherited `CGO_ENABLED=1`. Metadata review
  caught the mismatch before deployment; the accepted candidate was rebuilt
  with the existing static `CGO_ENABLED=0` contract and had zero dynamic
  links.
- The first atomic deployment attempt stopped before copying or restarting
  because the scoped rollback parent did not yet exist. A mode-0700 parent was
  created, the retry backed up the old binary, and only then was the same
  Server unit restarted.
- The first final ASD health oracle expected a plain `healthy` body although
  the API correctly returned `{"status":"ok"}`; the mechanical retry then
  assumed `jq`, which is not installed on ASD. The final dependency-free exact
  JSON-body gate passed, with the same Server PID and zero restarts throughout.
- The original private Ed25519 test certificate caused a mobile Firefox
  `SSL_ERROR_NO_CYPHER_OVERLAP` result and required manual trust installation.
  A temporary RSA private-CA replacement was generated and compatibility-tested
  but deliberately rolled back and its temporary private keys deleted after
  the browser trust-boundary design was corrected to use a public IP
  certificate.
- After the certificate migration, the first direct-IP login correctly
  returned 403 because the Server still allowed only the retired hostname as
  its exact Origin. Only `AISUMMONER_BASE_URL` in the private runtime
  environment was changed; the same Server unit was restarted and the full
  direct-IP login/cookie/devices/wrong-Origin matrix then passed. The first
  post-change replay used the obsolete `/api/auth/login` path and returned
  404; the production `/api/v1/auth/login` path was then verified instead.
- One local login-oracle wrapper used Bash-only lowercase expansion under zsh
  and stopped after the HTTP request. Its private temporary directory was
  removed by the exit trap; the portable mechanical replay produced the
  accepted evidence above.
- The first ACME webroot probe returned the existing SPA body with status 200,
  so exact sentinel comparison correctly rejected it. The corrected scoped
  directory probe returned the exact sentinel and removed it afterward.
- Production certificate issuance succeeded, but the first chain-verification
  invocation omitted `-untrusted chain.pem`; no certificate was deployed until
  the corrected system-root verification passed.
- Certbot renewal simulation inserted a documented 266-second random delay.
  The tool output channel ended before the remote job, so it was not treated as
  success until the exact running container naturally exited and the rotated
  Certbot log recorded that all simulated renewals succeeded.
- The final certificate comparison first looked in the system
  `/etc/letsencrypt` path. Certbot is intentionally isolated under this
  Task011 state root, so that path was absent. The deployed Caddy chain then
  matched the actual scoped Certbot live chain byte for byte; chain, IP SAN,
  health and renewal checks all passed.
