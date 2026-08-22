# Task010 Public TCP 443 Unblock Runbook

## Scope

This is the sole external action required by the Task010 revision-0
`BLOCKED` review. It is not a repository implementation change and it does
not authorize changing nginx, port 80, AISummoner internal ports, or any
unrelated workload.

Resolved from Tencent Cloud instance metadata on 2026-08-13:

| Field | Value |
| --- | --- |
| Region | `ap-shanghai` |
| Zone | `ap-shanghai-2` |
| Instance | `ins-rjio1q3l` |
| Public IPv4 | `122.51.70.33` |
| Attached security group | `sg-oktx2bfo` |
| Security-group name | `lh-1443973450-lhins-if4flxmw` |

The instance has no global IPv6 address or IPv6 default route. Metadata lists
one attached security group. Both custom and service-role CAM credential
directory endpoints returned 404; the workstation also has no Tencent Cloud
CLI, credential environment or client configuration. Therefore the current
agent cannot safely perform this mutation from the host.

## Required Console Change

An operator with Tencent Cloud account/security-group authority should:

1. Open the Tencent Cloud security-group console in region `ap-shanghai`.
2. Select `sg-oktx2bfo` and verify that it is attached to
   `ins-rjio1q3l`. Stop if either identifier differs.
3. Inspect inbound rule priority. Do not modify or delete any existing rule.
4. Add one inbound IPv4 rule only:

   | Setting | Value |
   | --- | --- |
   | Type | Custom or HTTPS |
   | Source | `0.0.0.0/0` |
   | Protocol/port | `TCP:443` |
   | Policy | Allow |
   | Note | `AISummoner Task010 public TLS acceptance` |

   The allow rule must have effective priority before any broader matching
   deny. `0.0.0.0/0` is needed for publicly trusted TLS-ALPN issuance and the
   planned public demo; restricting it to the tester IP would not prove the
   public acceptance boundary.
5. Do **not** use “allow all ports”, do not add 8088/14096/14097, do not add an
   IPv6 rule, and do not edit/reload/stop nginx on port 80.
6. If a separate Tencent Cloud Firewall policy is enabled for this EIP, add
   the same narrow TCP 443 allow there only after confirming the security-group
   rule alone does not restore reachability. Do not broaden other policies.

Tencent Cloud documents inbound HTTPS as source `0.0.0.0/0`, protocol
`TCP:443`, policy Allow:

- <https://cloud.tencent.com/document/product/213/112614/>
- <https://cloud.tencent.com/document/product/213/112626>

The security group is stateful, so no matching outbound response rule should
be needed when normal outbound traffic is already allowed.

## Operator Handoff

After the rule is effective, report only that TCP 443 is open; do not send
console credentials, API keys, screenshots containing account data, or an
export of all security-group rules. The integration agent will then:

1. confirm port 443 is free and nginx hashes/unrelated workloads are unchanged;
2. recreate fresh 0600 secrets and the exact unprivileged scoped runtime;
3. request a public certificate for `122-51-70-33.sslip.io` using TLS-ALPN;
4. run the production lzr Client directly over strict public WSS without
   `--dev` or custom test-CA trust;
5. rerun outside-in public health, Browser HTTPS login/Secure cookie, Terminal
   WSS and Agent SSE from local and lzr;
6. reconfirm 8088/14096/14097 are loopback-only and unreachable outside ASD;
7. freeze Task010 revision evidence and return it to independent review.

## Rollback

If the public rerun fails for an unrelated reason or the demo will not remain
online, remove only the exact rule whose note is
`AISummoner Task010 public TLS acceptance`. Do not delete or replace the
security group. The host-side Task010 runtime is already stopped and its
cleanup record is in
[`mvp-0-2026-08-13-cleanup.md`](mvp-0-2026-08-13-cleanup.md).

## Read-Only Discovery Evidence

- Direct fixed-IP SSH reached the expected ASD host as uid 1001.
- Metadata returned one instance, region/zone, public/private IPv4 and one
  attached security group. Resource identifiers above are non-secret.
- `cam/security-credentials/` and
  `cam/service-role-security-credentials/` returned 404. No credential JSON
  path was accessed.
- No cloud CLI, Tencent credential environment or Tencent client config was
  present locally; no network rule was modified.
- The first local discovery shell reused zsh's special `path` variable and
  cleared that subprocess's command search path. It performed no mutation and
  was rerun with a task-specific variable. The successful results above are
  from the corrected read-only command.
