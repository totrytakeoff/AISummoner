# Remote Client Private IPC v1

- 状态：Task015 冻结候选，供 Task016 Qt GUI 消费
- 传输：Linux Unix domain stream socket
- 信任边界：同 UID 本机进程；不是远程 API

## Socket 与 framing

- 默认路径：`<absolute-data-dir>/client.sock`。
- data directory 必须是 daemon UID 拥有、非 symlink、精确 mode `0700` 的目录；
  socket 必须是同 UID 拥有、精确 mode `0600` 的 Unix socket。
- daemon 在读取请求前使用 `SO_PEERCRED` 核对 peer UID。文件权限不是唯一认证。
- 一条连接只处理一个 request/response；每个 JSON object 后跟一个 `\n`。
- request/response（含换行）上限 `64 KiB`，处理器最多并发 8 个；操作 deadline
  为 5 秒，另有 250ms 的有界错误响应写回窗口。
- JSON 拒绝 unknown field、duplicate key、多值、错误类型和尾随内容。

## Envelope

请求：

```json
{"version":1,"id":"req_example","method":"status.get","params":{}}
```

成功：

```json
{"version":1,"id":"req_example","ok":true,"result":{}}
```

失败：

```json
{"version":1,"id":"req_example","ok":false,"error":{"code":"INVALID_REQUEST","message":"invalid local request"}}
```

`id` 为 5–64 字符的 ASCII 字母、数字、`_`、`-`。客户端必须同时核对
version 和 response id。

## Methods

### `status.get`

params 为 `{}`，result 为：

```json
{
  "device_id":"dev_...",
  "device_name":"workstation",
  "client_version":"0.1.0",
  "server_origin":"https://example.invalid",
  "phase":"online",
  "online_since":"2026-08-23T02:00:00Z",
  "pairing":{"code":"LOCAL-ONLY","expires_at":"2026-08-23T02:10:00Z","expired":false},
  "active_sessions":1,
  "updated_at":"2026-08-23T02:00:00Z"
}
```

- `phase` 只能是 `starting|connecting|online|retrying|paused|stopped|error`。
- `online_since`、`retry_at`、`pairing` 和 `last_error_category` 按状态省略。
- pairing 过期后 `code` 被清空且 `expired=true`；无 offer 时整个 pairing 省略。
- **只有此方法可能返回 pairing code。** GUI 不得把完整 response 写日志、crash
  artifact、analytics 或测试快照。

### `events.list`

params：

```json
{"after_sequence":0,"limit":100}
```

result：

```json
{"events":[{"sequence":1,"at":"2026-08-23T02:00:00Z","kind":"tunnel.online","level":"info","summary":"Connected to control service"}],"next_sequence":1}
```

`after_sequence >= 0`，`limit` 为 1–200。事件最多保留 200 条，sequence 单调递增。
事件内容仅是 daemon 定义的固定摘要，永不包含 pairing code、Server URL、命令/cwd、
Terminal/Agent 内容、SSH key、credential、raw transport error 或 request payload。

### `daemon.pause`

params/result 均为 `{}`。成功响应表示当前 Tunnel 和已接受的 SSH stream 已 joined
关闭，自动重连已禁用，状态已经是 `paused`。

### `daemon.resume`

params/result 均为 `{}`。幂等地恢复单一连接循环；不会创建重复 worker。

### `pairing.refresh`

params 为 `{}`。仅当前存在有效或已过期 pairing offer 时可调用。成功 result：

```json
{"closes_active_sessions":true}
```

GUI 必须在发送前据此合同明确确认刷新会关闭活跃控制会话。成功响应表示旧 Tunnel
已经 joined 关闭，新连接循环已获准启动；新 code 随后由 `status.get` 出现。

## 固定错误码

| code | 含义 |
|---|---|
| `INVALID_REQUEST` | envelope/params/framing 不合法 |
| `METHOD_NOT_FOUND` | method 不在 v1 allowlist |
| `TIMEOUT` | joined 本机操作未在上限内完成 |
| `NO_PAIRING_OFFER` | 当前 paired/无 offer，不能刷新 |
| `DAEMON_UNAVAILABLE` | Core 未运行或已经停止 |
| `OPERATION_FAILED` | 其他已脱敏操作失败 |
| `INTERNAL_ERROR` | daemon 无法编码固定响应 |

错误 message 固定且不可拼入参数、路径、配对码、endpoint 或底层错误文本。

## Qt 消费规则

- 使用异步 `QLocalSocket`；UI thread 不做阻塞 connect/read/wait。
- 每次调用新建连接，收齐一行后关闭；先验证 frame size、version、id 和 success/error
  互斥，再更新 UI model。
- `status.get` 可每秒轮询，`events.list` 用 `next_sequence` 增量拉取；断线后重新取
  snapshot，再从最后 sequence 续读。
- GUI 退出只关闭本机 socket，不发送 pause；Pause 是显式用户动作。
