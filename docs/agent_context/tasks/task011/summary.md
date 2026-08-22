---
task_id: task011
type: summary
status: ready_for_user_testing
from: implementation
to: human
revision: 0
review_required: false
---

# Task 011 Summary

All three user-requested outcomes are ready:

1. ASD Server is active at `https://122-51-70-33.sslip.io:10001` in Fake mode,
   behind strict private-test-CA TLS. It preserves nginx, TCP 80 and all
   unrelated containers.
2. A verified Linux x86_64 AppImage is available under the private Task011
   handoff. Ubuntu 24.04 non-root usage and root refusal passed.
3. The local MyArch Client is active as uid 1000, owns no listener, is connected
   to ASD, and has a one-time pairing code in a private file.

The private handoff README contains the administrator, CA, pairing and exact
status/stop instructions without exposing their values in source or chat.
Full evidence is in
`docs/acceptance/task011-test-deployment-2026-08-21.md`.

Known limits: the certificate uses a private short-lived test CA; Server uses
Fake rather than OpenCode; the AppImage is checksum-verified but unsigned; the
ASD Server/Caddy are intentionally transient and must be recreated after a
host reboot.
