# Task014 Web DeepSeek Key Entry Deployment

## Outcome

The authenticated AISummoner Agent page now provides a `Set up DeepSeek`
button at `https://122.51.70.33:10001`. The dialog asks only for the DeepSeek
API key; the Browser uses the fixed default model `deepseek-v4-flash`.

The key crosses only the existing same-origin HTTPS control plane and is kept
only in Server process memory. It is not written to SQLite, an environment
file, Browser storage, audit data, logs, URLs or responses. A Server restart
forgets it and the administrator must enter it again.

## Safe Deployment

- Scoped Server unit:
  `aisummoner-task011-server-20260821T090612Z.service`.
- Server remains non-root (`myself`, uid 1001) and listens only on
  `127.0.0.1:8088`.
- Public Caddy remains the sole listener on TCP 10001. Its container ID stayed
  `30fc8ec164a7` and it was not restarted or modified.
- nginx remained active on TCP 80. Its readable configuration-manifest digest
  stayed `d9c1f5b0a872c8bef5a4f226f789d33874826a281e394ed3169a4f040291c85b`.
- The scoped OpenCode container and all four unrelated ASD containers stayed
  running. TCP 10002 remained unused; OpenCode and Bridge remained loopback
  only on 14096 and 14097.
- The existing public Client connection returned after the Server restart;
  one established TCP 10001 connection was observed at the final gate.

The Server binary changed atomically from SHA-256
`e40afb9eb1c05bca6ae61ec623cc15475d407d47cac7e7dc7eba7204d652c648`
to
`0c3077e7de5968ae1eac6611f90986f9021f4782aae04bb338d23e44291fc7e4`.
The exact old binary is retained at:

```text
/home/myself/.local/state/aisummoner-task014/20260822T155831Z/aisummoner-server.old
```

## Live Oracles

- Loopback `/healthz`: HTTP 200.
- Strict public `https://122.51.70.33:10001/healthz`: HTTP 200.
- Public index references the new `assets/index-mLUQiaFg.js` bundle.
- The bundle contains the `Set up DeepSeek` interaction.
- Unauthenticated `POST /api/v1/agent-provider/deepseek`: HTTP 401, proving the
  exact route is live and remains behind administrator authentication.
- New Server PID `4156268` ran the exact candidate hash as uid 1001.

## Human Test

1. Open `https://122.51.70.33:10001` and sign in.
2. Open the online Device, then open `Agent`.
3. Select `Set up DeepSeek`.
4. Paste a newly issued DeepSeek API key and select `Use DeepSeek`. Do not paste
   the key into chat, screenshots or logs.
5. AISummoner creates a new `DeepSeek` conversation automatically. Send a
   prompt; approve any `remote_exec` command from its tool card as usual.
6. If the Server is restarted, repeat steps 3–4 because the key is intentionally
   memory-only.

The deployment proves the configuration surface and preserves the existing
runtime. A real provider Turn is intentionally left for the administrator's
private key entry and must not be claimed before that user-controlled test.

## Revision 2: Tool Loop Without A Cumulative Count Wall

The first private-key live test proved the full DeepSeek → approval → SSH →
Remote chain, but one Turn completed twelve tools and failed when the model
requested a thirteenth. Read-only SQLite aggregation inspected only Provider,
state and counts—not prompts, commands or output—and confirmed the local limit.

The cumulative Turn count was removed. A Turn is still bounded by its total
deadline and all existing approval, command-time, byte, response, idle,
serialization and cancellation controls. Tests proved 20 sequential tool steps
can reach a final answer and 20 concurrent callbacks remain serialized. A
single malformed Provider response is independently bounded at 64 tool calls.

The Server was atomically replaced again:

- Prior binary: `0c3077e7de5968ae1eac6611f90986f9021f4782aae04bb338d23e44291fc7e4`.
- Current binary: `480fddfdf77d2072fad4cacc32ddca98e693aeba24ea75d5c39af0e7f2c0c231`.
- Rollback directory:
  `/home/myself/.local/state/aisummoner-task014/20260822T164013Z`.
- Current unit PID at freeze: `4181458`, uid 1001.

Public health returned 200, the existing Client reconnected, and nginx's
manifest digest plus all Caddy/OpenCode/unrelated container identities remained
unchanged. Because the key is memory-only, this Server restart cleared it; the
administrator must enter the key again before retrying the long Turn.

The user accepted the MVP vertical slice after directly confirming Terminal
and DeepSeek Agent execution through the real Remote. No post-revision-2 live
rerun was requested: the count-wall removal is covered by the focused,
20-repeat, race and merged gates above, while later Agent call quality and UI
polish move to post-MVP work.
