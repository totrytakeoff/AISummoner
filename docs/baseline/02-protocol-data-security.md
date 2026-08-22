# AISummoner MVP-0 协议、数据与安全基线

状态：**已冻结**
版本：`0.1`
日期：`2026-08-12`

## 1. 协议原则

- 所有自有协议带 `version: 1`；
- 控制消息使用长度前缀 JSON，便于三天内实现和调试；
- Terminal payload 保持二进制，不做 Base64；
- 除由公钥确定性派生的 `device_id` 外，所有资源 ID 为不可预测随机 ID，并带资源前缀；
- Server 永远从已认证上下文确定 user/device，不信任客户端提交的所有者字段；
- 未知消息类型返回协议错误；版本不兼容立即断开；
- 每个请求都有超时、大小上限和明确关闭语义。

## 2. Tunnel 建连与设备认证

### 2.1 Transport

Remote 连接：

```text
GET /api/v1/tunnel
Upgrade: websocket
```

Upgrade 后使用 WebSocket binary message 作为可靠字节流，再建立 yamux Client/Server session。

预认证连接必须在 10 秒内完成 challenge/response；Server 对同时处于预认证阶段的连接数设置上限，并对来源地址做基础速率限制，防止未认证连接耗尽资源。

### 2.2 Stream Header

每个 yamux stream 首先发送：

```text
uint32 big-endian JSON length
JSON bytes，最大 64 KiB
```

Header：

```json
{
  "version": 1,
  "kind": "control",
  "request_id": "req_..."
}
```

MVP-0 的 `kind` 只有：

- `control`：Remote 创建，连接期唯一且常驻；
- `ssh`：Server 创建，每个 Terminal/Agent exec 一个。

首个 stream 必须是 `control`，否则 Server 关闭整个 yamux session。

### 2.3 Challenge/Response

Control stream 上的认证顺序：

```text
ClientHello
  ← ServerChallenge
DeviceProof
  ← Authenticated
  ← PairingOffered（未绑定设备才发送）
Heartbeat ↔ HeartbeatAck
```

`ClientHello`：

```json
{
  "version": 1,
  "type": "client.hello",
  "request_id": "req_...",
  "payload": {
    "device_id": "dev_...",
    "device_public_key": "base64url-raw-ed25519",
    "device_name": "workstation",
    "platform": "linux",
    "arch": "amd64",
    "client_version": "0.1.0"
  }
}
```

`device_id` 定义为：

```text
"dev_" + base32lower(SHA-256(raw Ed25519 public key))[0:32]
```

Server 必须重新计算并比较，禁止由 Client 任意指定身份。

ServerChallenge payload 包含 32 字节 CSPRNG nonce。签名原文为：

```text
"aisummoner-device-auth-v1\x00" || nonce || raw_device_public_key
```

Server 验证 Ed25519 签名后才将连接放入 Connection Manager。

`Authenticated` 同时下发当前 Tunnel 唯一的 SSH Client 公钥：

```json
{
  "version": 1,
  "type": "server.authenticated",
  "request_id": "req_...",
  "payload": {
    "connection_id": "conn_...",
    "ssh_client_public_key": "authorized-key-format",
    "heartbeat_interval_ms": 5000
  }
}
```

同一 Device 出现第二个已认证连接时，采用 **newest wins**：Server 原子替换 Connection Manager 中的连接并关闭旧连接。

## 3. Pairing

未绑定设备认证成功后，Server 生成 8 位无歧义 Base32 Pairing Code，例如：

```text
K7HF-92PQ
```

规则：

- 去除大小写和 `-` 后规范化；
- 一次性；
- 10 分钟过期；
- 每台未绑定设备同一时刻只有一个 Active Code；
- 数据库只保存 `HMAC-SHA256(AISUMMONER_PAIRING_SECRET, normalized_code)`；
- 明文只通过已认证 control stream 展示给 Remote；
- Claim 成功在一个数据库事务中完成绑定和消费；
- 同一来源连续失败需要速率限制。

配对不改变设备长期密钥。

## 4. 心跳和连接状态

Remote 每 5 秒发送：

```json
{
  "version": 1,
  "type": "device.heartbeat",
  "request_id": "req_...",
  "payload": {
    "sent_at": "2026-08-12T12:00:00Z"
  }
}
```

Server 只在消息通过当前已认证 control stream 到达时更新 `last_seen_at`。

状态规则：

```text
Disconnected → Connecting → Authenticating → Online
      ▲                                      │
      └──────── backoff ← Disconnected ──────┘
```

- 15 秒未收到心跳：Offline 并关闭 Connection；
- 重连间隔：1s、2s、4s、8s、15s，之后维持 15s；
- 每次加入 ±20% jitter；
- 成功在线 30 秒后重置 backoff。

## 5. Browser API

### 5.1 Auth

```text
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/me
```

登录成功使用随机 Session Cookie：

- `HttpOnly`；
- `Secure`（生产）；
- `SameSite=Strict`；
- 固定过期时间 24 小时；
- 数据库只保存 Token SHA-256 digest。

管理员密码使用带参数的 PHC 格式 Argon2id hash。MVP-0 参数固定为 64 MiB memory、3 iterations、2 lanes、16-byte salt、32-byte output，并使用 constant-time compare。首次启动成功创建管理员后，不再要求保留 bootstrap 密码环境变量。

所有状态修改请求验证 `Origin`。MVP-0 不支持跨站前端部署。

### 5.2 Devices/Pairing

```text
GET    /api/v1/devices
GET    /api/v1/devices/{device_id}
POST   /api/v1/pairings/claim
DELETE /api/v1/devices/{device_id}
```

Claim body：

```json
{ "code": "K7HF-92PQ" }
```

### 5.3 Terminal

```text
GET /api/v1/devices/{device_id}/terminal
Upgrade: websocket
```

WebSocket frame 约定：

- binary：Terminal stdin/stdout 原始字节；
- text JSON：控制消息。

Resize：

```json
{
  "type": "terminal.resize",
  "cols": 120,
  "rows": 36
}
```

限制：

- cols 1–500；
- rows 1–300；
- 单个 WebSocket frame 最大 64 KiB；
- 每个用户最多 4 个并发 Terminal；
- 浏览器关闭后 Server 关闭 SSH session 和 yamux stream。

### 5.4 Agent

```text
POST /api/v1/devices/{device_id}/agent-sessions
GET  /api/v1/devices/{device_id}/agent-sessions
GET  /api/v1/agent-sessions/{session_id}
POST /api/v1/agent-sessions/{session_id}/messages
GET  /api/v1/agent-sessions/{session_id}/events
POST /api/v1/tool-calls/{tool_call_id}/decision
POST /api/v1/agent-provider/deepseek
```

创建 Session：

```json
{
  "approval_mode": "per_command"
}
```

允许的 `approval_mode`：

- `per_command`：默认，每次执行前暂停；
- `full_access`：只对当前 Session 有效，创建时需 WebUI 明确确认。

SSE 事件类型：

```text
session.state
response.reasoning.delta
response.reasoning.done
response.text.delta
response.text.done
tool_call.pending
tool_call.started
tool_call.output
tool_call.completed
turn.completed
turn.failed
```

每个 SSE 包含 `event_id`、`session_id`、`created_at` 和类型相关 payload。MVP-0 允许 Server 重启后中断正在运行的 Turn，不要求 SSE replay。

Device 级 `GET .../agent-sessions` 返回当前 owner 可见的最新非 revoked Session
快照，包含已持久化的 user、reasoning、assistant 消息与工具调用；无 Session
返回标准 404。Browser 用它恢复对话；无历史或显式“新对话”时直接
创建默认 `per_command` Session，不显示启动模式向导。只有在待审批工具上
显式确认 `approve_session` 才把当前 Session 提升为 `full_access`。
Provider reasoning 必须先在 Server 归一化并与 assistant 最终回答分开持久化，
Browser 不解析 Provider 私有事件结构。

工具决策：

```json
{ "decision": "approve_once" }
```

MVP-0 允许：`approve_once`、`approve_session`、`deny`。

单管理员可以通过受认证且通过精确 `Origin` 校验的 Provider 配置请求提交：

```json
{
  "api_key": "<DeepSeek API key>",
  "model": "deepseek-v4-flash"
}
```

请求体最多 16 KiB；Key 最多 4096 字节、模型最多 256 字节，均必须是非空
可见 ASCII。Browser 不可提供 Provider URL；Server 固定使用官方 DeepSeek
HTTPS origin 且禁止 redirect。成功返回空的 `204`，不回显 Key/model，并把
新 Adapter 只保留在进程内存。后续新 Session 记录 `provider=deepseek`；旧
Session 保留原 Provider，运行中的 Turn 不被切换。Key 不写 SQLite、日志、
审计、环境文件、URL、Browser storage 或错误响应，Server 重启后失效。

## 6. HTTP 错误格式

所有 JSON API 错误统一为：

```json
{
  "error": {
    "code": "DEVICE_OFFLINE",
    "message": "device is offline",
    "request_id": "req_..."
  }
}
```

`message` 可展示给用户，但不能包含 secret、SQL、stack trace 或远程环境敏感细节。详细错误仅写入 Server 日志并关联 `request_id`。

## 7. SQLite 数据模型

SQLite 使用 WAL、foreign keys 和 busy timeout。最小表：

### users

```text
id                  TEXT PRIMARY KEY
username            TEXT UNIQUE NOT NULL
password_hash       TEXT NOT NULL
created_at          DATETIME NOT NULL
```

MVP-0 只创建一个管理员用户。

### web_sessions

```text
id                  TEXT PRIMARY KEY
user_id             TEXT NOT NULL REFERENCES users(id)
token_digest        BLOB UNIQUE NOT NULL
expires_at          DATETIME NOT NULL
created_at          DATETIME NOT NULL
```

### devices

```text
id                  TEXT PRIMARY KEY
public_key          BLOB UNIQUE NOT NULL
owner_user_id       TEXT NULL REFERENCES users(id)
name                TEXT NOT NULL
platform            TEXT NOT NULL
arch                TEXT NOT NULL
client_version      TEXT NOT NULL
created_at          DATETIME NOT NULL
paired_at           DATETIME NULL
last_seen_at        DATETIME NULL
```

Online 状态不写成持久字段，由 Connection Manager 和 `last_seen_at` 派生。

### pairing_codes

```text
id                  TEXT PRIMARY KEY
device_id           TEXT NOT NULL REFERENCES devices(id)
code_digest         BLOB UNIQUE NOT NULL
expires_at          DATETIME NOT NULL
consumed_at         DATETIME NULL
created_at          DATETIME NOT NULL
```

### agent_sessions

```text
id                  TEXT PRIMARY KEY
user_id             TEXT NOT NULL REFERENCES users(id)
device_id           TEXT NOT NULL REFERENCES devices(id)
approval_mode       TEXT NOT NULL
provider            TEXT NOT NULL
external_session_id TEXT NULL
state               TEXT NOT NULL
created_at          DATETIME NOT NULL
updated_at          DATETIME NOT NULL
```

### agent_messages

```text
id                  TEXT PRIMARY KEY
session_id          TEXT NOT NULL REFERENCES agent_sessions(id)
role                TEXT NOT NULL
content             TEXT NOT NULL
created_at          DATETIME NOT NULL
```

### tool_calls

```text
id                  TEXT PRIMARY KEY
session_id          TEXT NOT NULL REFERENCES agent_sessions(id)
name                TEXT NOT NULL
arguments_json      TEXT NOT NULL
status              TEXT NOT NULL
decision            TEXT NULL
exit_code           INTEGER NULL
output_excerpt      TEXT NULL
created_at          DATETIME NOT NULL
completed_at        DATETIME NULL
```

`output_excerpt` 最多保存 8 KiB；提供给当前模型 Turn 的临时输出可到 256 KiB，但默认不完整持久化。

### audit_events

```text
id                  TEXT PRIMARY KEY
actor_user_id       TEXT NULL
device_id           TEXT NULL
event_type          TEXT NOT NULL
metadata_json       TEXT NOT NULL
created_at          DATETIME NOT NULL
```

记录登录、配对、解绑、Tunnel 认证、Terminal 打开/关闭、Agent 审批和命令结果。禁止记录 Terminal 输入、密码、Cookie、私钥或 API Key。

## 8. 授权规则

每个面向用户的 Device、Terminal、Agent handler 必须执行相同检查：

```text
authenticated_user.id == devices.owner_user_id
```

要求：

- 不用“设备 ID 难猜”代替授权；
- Agent Session 和 Tool Call 必须再次通过关联表验证 user；
- 配对只允许绑定 `owner_user_id IS NULL` 的设备；
- 解绑后已有 Terminal/Agent Session 立即关闭；
- Device Offline 时不创建 SSH stream；
- Remote 端不接受来自 control stream 指定临时公钥以外的 SSH Client。

## 9. Agent 执行安全

- 默认 `per_command`；
- `full_access` 只存于 Agent Session，不设全局永久开关；
- Tool Call 超时后关闭 SSH stream；
- 合并输出超过 256 KiB 时截断并明确告知模型；
- 每个 Turn 最长 5 分钟；
- 每个 Turn 最多 12 次 Tool Call；
- OpenCode sidecar 只监听 loopback，使用 Basic Auth，内置本地工具全部禁用；
- DeepSeek Adapter 只通过校验后的 HTTPS origin 出站，禁止 redirect，不记录
  API Key、Provider 原始请求/响应或 reasoning，且只向模型公布
  Server 绑定的 `remote_exec`；Thinking 工具循环必须回传当轮
  `reasoning_content` 和非 null assistant content，并省略 Provider 当前拒绝的
  `tool_choice`；
- Web 配置 DeepSeek 时，必须先完成同源 Origin 与管理员 Session 校验，再读取
  有界 JSON；配置成功只更新内存 Adapter registry，不持久化凭据。浏览器表单
  不使用 localStorage/sessionStorage，取消或成功后清除输入；
- `remote_exec` bridge 不接受 host/device 参数，目标由 external session 映射确定；
- Remote `exec` 使用 `$SHELL -lc <command>`，权限等于 Remote Client 用户；
- cwd 使用 SSH env request 设置 `exec.Cmd.Dir`，必须存在且为目录；
- 不使用命令 denylist 冒充安全沙箱；高风险控制依赖用户审批和 OS 用户权限。

## 10. MVP-0 已知安全缺口

这些缺口不阻塞三天 Demo，但阻塞公开生产部署：

- Agent Runtime 尚未容器隔离；
- 单节点内存 Connection Manager；
- 无完整 Secret Manager 和 key rotation；
- 无细粒度文件/命令 Capability Policy；
- 无分布式限流；
- 无第三方安全审计和渗透测试；
- 无供应链签名与自动升级；
- 无完整备份、恢复和数据保留策略。
