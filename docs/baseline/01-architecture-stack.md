# AISummoner MVP-0 架构与技术栈基线

状态：**已冻结**
版本：`0.1`
日期：`2026-08-12`

## 1. 架构结论

MVP-0 采用：

> **Go Remote Client + Go 单体 Server + React WebUI + WSS/yamux + Embedded SSHD + SQLite + OpenCode sidecar**

核心结构：

```text
┌──────────────── Browser ────────────────┐
│ React WebUI                            │
│ Device / Pairing / xterm.js / Chat     │
└───────┬──────────────┬─────────────┬───┘
        │ REST         │ WebSocket   │ SSE
        │              │ Terminal    │ Agent events
        ▼              ▼             ▼
┌──────────────── Go Server ──────────────┐
│ Auth / Registry / Pairing               │
│ Tunnel Gateway / Terminal Gateway       │
│ Agent Loop / Audit / SQLite             │
└──────────────────┬──────────────────────┘
                   │ WSS
                   │ yamux
                   │ control + N SSH streams
                   ▼
┌────────────── Go Remote Client ─────────┐
│ Device Identity / Heartbeat / Reconnect │
│ Embedded SSH Server / PTY / Exec        │
└──────────────────┬──────────────────────┘
                   ▼
             User Shell / OS
```

## 2. 组件职责

### 2.1 WebUI

只负责展示和用户交互：

- 登录；
- 输入 Pairing Code；
- 设备列表与状态；
- Terminal 输入、输出和 resize；
- Agent Chat 和审批；
- 展示工具命令、执行状态与结果。

WebUI 不持有 Device 私钥或 SSH 私钥，不直接连接 Remote。管理员可以在受
保护的 Agent 设置表单中短暂输入 Provider API Key；该值只存在于挂载中的
密码输入框和同源 HTTPS 请求，取消或提交成功即清空，绝不进入浏览器持久
存储或 Provider 直连。

### 2.2 Server

MVP-0 保持一个 Go 单体进程，内部按 package 分层：

- Auth：单管理员登录和 Web Session；
- Device Registry：设备身份、归属和元数据；
- Pairing：短期配对码生成、校验和消费；
- Tunnel Gateway：Remote 长连接和在线 Connection Manager；
- Terminal Gateway：Browser WebSocket 与 SSH Channel 桥接；
- Agent Runtime：模型调用、工具循环、审批和事件流；
- Store：SQLite 和 migration；
- Audit：安全相关事件记录；
- Static Web：生产构建后嵌入 Server 二进制。

MVP-0 不拆微服务。

### 2.3 Remote Client

Remote Client 是一个普通用户权限的 Go 单二进制：

- 首次运行生成 Ed25519 Device Identity；
- 主动连接 Server WSS Endpoint；
- 完成挑战签名；
- 显示 Server 下发的 Pairing Code；
- 维护心跳与自动重连；
- 接收 Server 打开的逻辑流；
- 在逻辑流上提供 Embedded SSH Server；
- 以当前进程用户身份启动 Shell、PTY 和命令。

Remote Client 不监听公网或 localhost TCP 端口。

## 3. 技术栈

| 领域 | 决策 | MVP 理由 |
|---|---|---|
| Client | Go | 单二进制、并发网络模型直接、交叉编译简单 |
| Server | Go `net/http` | Client/Server 共享协议类型，减少框架与生成代码 |
| WebUI | React + TypeScript + Vite | 快速生成和调试交互界面 |
| UI | Tailwind CSS + shadcn/ui | 以复制式组件快速完成 MVP，不引入重型设计系统 |
| Routing | React Router | 登录、设备列表、设备详情三类页面足够 |
| Terminal | `@xterm/xterm` + fit addon | 成熟浏览器终端和 resize 支持 |
| Browser Terminal Transport | WebSocket | 全双工、二进制终端数据 |
| Agent Event Transport | SSE | 单向流式事件足够，浏览器实现简单 |
| Remote Transport | HTTPS/WSS | 便于穿过代理和常见防火墙 |
| Multiplex | `github.com/hashicorp/yamux` | 在一个可靠连接上承载 control 和多 SSH stream |
| Go WebSocket | `github.com/coder/websocket` | 支持 `net.Conn` 包装和 `context.Context` |
| SSH | `golang.org/x/crypto/ssh` | 同时提供 SSH Client/Server 能力 |
| PTY | `github.com/creack/pty` | Linux PTY 与窗口 resize |
| Database | SQLite WAL | 单节点零外部依赖 |
| SQLite Driver | `modernc.org/sqlite` | 纯 Go，避免 MVP 交叉编译引入 CGO |
| Password Hash | Argon2id | 不存明文管理员密码 |
| Agent | OpenCode headless sidecar | 复用 Agent Loop、Session、事件和免费模型入口 |
| Deployment | Docker Compose + Caddy | 自动 TLS 和最小部署结构 |
| Logging | Go `slog` JSON | 标准库、结构化、低依赖 |
| Go Tests | `go test` | package、集成和 race test |
| Browser E2E | Playwright | 覆盖配对、设备、Terminal/Agent 冒烟链路 |

依赖版本不在文档中手工固定。首次 scaffold 时选择当日稳定版并提交 `go.mod`、`go.sum` 和前端 lockfile；后续以 lockfile 为可复现基准。

Agent 使用 OpenCode headless Server 的 Session API 与事件流。独立 Workspace 中的自定义 `remote_exec` Tool 只回调 AISummoner 的 loopback bridge；具体免费模型不写死，通过配置选择。真实 OpenCode 测试允许外部端点处于限流或不可用状态，确定性测试使用 Fake Adapter。

## 4. Tunnel 分层

```text
TCP
└── TLS
    └── WebSocket binary transport
        └── yamux session
            ├── control stream（Client 打开，连接期常驻）
            ├── ssh stream（Server 打开，Terminal）
            ├── ssh stream（Server 打开，Agent exec）
            └── future streams（MVP-0 不实现）
```

WebSocket 被包装为流式 `net.Conn`，再交给 yamux。yamux 只承担流拆分、流控和背压；业务身份、授权和 stream 类型由 AISummoner 协议负责。

MVP-0 不使用 QUIC，不自行设计 multiplex frame。

## 5. SSH 模型

每个 Terminal 或 Agent Tool Call 使用一个独立 yamux stream 和一个独立 SSH transport：

```text
Server ssh.Client
  → yamux stream
  → Remote ssh.ServerConn
  → PTY Shell 或 non-PTY exec
```

### SSH Host Key

Remote 使用 Device Ed25519 Identity 作为 SSH Host Key。Server 验证 SSH Host Key 必须与 Device Registry 中登记的公钥一致，禁止 `InsecureIgnoreHostKey`。

### SSH Client Authentication

Tunnel 认证成功后，Server 为当前连接生成临时 Ed25519 SSH Client Key，并通过已认证 control stream 把公钥下发给 Remote。Remote SSHD 只接受：

- 逻辑用户名：`aisummoner`；
- 当前连接临时公钥；
- 当前 yamux session 内由 Server 打开的 stream。

私钥只驻留 Server 内存；Tunnel 断开后公私钥失效。

### MVP-0 SSH 能力

实现：

- `session` channel；
- `shell`；
- `exec`；
- `pty-req`；
- `window-change`；
- stdin/stdout/stderr；
- 退出状态；
- 最小 signal 支持。

不实现：

- SFTP/SCP；
- TCP forwarding；
- SSH agent forwarding；
- X11 forwarding；
- 用户自带 SSH key；
- SSH Certificate Authority。

## 6. Agent Runtime

MVP-0 不启动 Codex、Claude Code 或其他 CLI Agent。Server 编排一个同机 OpenCode sidecar：

```text
User message
  → OpenCode Session API / event SSE
  → OpenCode custom tool: remote_exec
  → approval gate
  → SSH exec on selected Remote
  → function output
  → Responses API continues
  → final answer
```

Adapter 边界必须存在，并实现 OpenCode 与 Fake 两种 Adapter：

```go
type Adapter interface {
    Run(ctx context.Context, req RunRequest, sink EventSink) error
}
```

MVP-0 验收后，已接受的 ADR-0003 允许在同一 Adapter 边界内增加
直接 Provider。当前 DeepSeek Adapter 直接消费有界 Chat-Completions/SSE，
但不获得 Server 本地 shell、文件系统或 Device 选择权；它与 OpenCode
共用 AISummoner 的 owner、审批、`remote_exec`、SSH、持久会话和 Web
事件边界。Fake 仍只是确定性测试 Adapter。

OpenCode 只能使用 Server 明确注册的远程工具，不能获得 Server 本地 shell。每个产品 Session 绑定一个 OpenCode Session、一个空 Workspace 和一个固定 Remote Device。

MVP-0 唯一工具：

```json
{
  "name": "remote_exec",
  "arguments": {
    "command": "uname -a",
    "cwd": "/home/user",
    "timeout_ms": 30000
  }
}
```

- `command`：必填，1–8192 字节；
- `cwd`：可选，必须是 Remote 上的绝对路径；
- `timeout_ms`：可选，限制在 1000–60000，默认 30000；
- 单次返回给模型的 stdout+stderr 上限：256 KiB；
- 每个用户消息最多 12 次工具调用。

Remote SSHD 通过受限的 `AISUMMONER_CWD` SSH env request 设置 `exec.Cmd.Dir`，不通过拼接 `cd ... &&` 实现 cwd。

## 7. 目标代码布局

```text
AISummoner/
├── cmd/
│   ├── aisummoner-server/
│   └── aisummoner-client/
├── internal/
│   ├── agent/
│   ├── audit/
│   ├── auth/
│   ├── config/
│   ├── device/
│   ├── pairing/
│   ├── protocol/
│   ├── sshserver/
│   ├── store/
│   ├── terminal/
│   └── tunnel/
├── migrations/
├── web/
│   ├── src/
│   └── package.json
├── deploy/
│   ├── Caddyfile
│   ├── compose.yaml
│   └── Dockerfile
├── docs/
├── go.mod
└── Makefile
```

Go module path在 scaffold 时使用仓库最终 Git Remote 地址，这是开工前唯一允许填写的仓库标识占位项。

## 8. 运行与配置

Server 最少需要：

```text
AISUMMONER_BASE_URL
AISUMMONER_LISTEN_ADDR
AISUMMONER_DATA_DIR
AISUMMONER_ADMIN_PASSWORD
AISUMMONER_SESSION_SECRET
AISUMMONER_PAIRING_SECRET
AISUMMONER_AGENT_ADAPTER
AISUMMONER_DEEPSEEK_URL
AISUMMONER_DEEPSEEK_API_KEY
AISUMMONER_DEEPSEEK_MODEL
AISUMMONER_OPENCODE_URL
AISUMMONER_OPENCODE_USERNAME
AISUMMONER_OPENCODE_PASSWORD
AISUMMONER_OPENCODE_MODEL
AISUMMONER_AGENT_BRIDGE_SECRET
```

约束：

- `AISUMMONER_ADMIN_PASSWORD` 只在数据库尚无管理员的首次启动时必填；成功 bootstrap 后不再依赖该环境变量；
- Secret 只通过环境或 secret file 注入；
- `fake`、`deepseek`、`opencode` 只读取各自所需的条件配置；DeepSeek
  要求 HTTPS origin、新轮换的 API Key 和显式模型，且不启动
  OpenCode/Bridge；
- 交互式单管理员测试可从 Agent 页只提交 DeepSeek Key，Browser 自动使用
  当前默认模型；非 Browser API 调用仍可显式提交模型。Server 只使用固定官方
  DeepSeek HTTPS origin，将 Adapter 与 Key 保留在进程内存，并让后续新
  Session 绑定 DeepSeek；重启后必须重新提交。该入口不替代无人值守
  环境/secret-file 配置；
- 启动日志只显示配置是否存在，不显示值；
- 生产模式拒绝明文 `http://` Base URL；
- 开发模式可以使用 localhost HTTP/WS，但必须显式设置 `AISUMMONER_DEV_MODE=1`。

Remote 默认数据目录：

```text
~/.local/share/aisummoner/
├── device.json
└── device_ed25519
```

私钥文件权限必须为 `0600`。
