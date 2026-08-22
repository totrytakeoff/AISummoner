# AISummoner 三天 MVP-0 实施与验收计划

状态：**已冻结**
版本：`0.1`
日期：`2026-08-12`

## 1. 估算口径

计划假设：

- 一位有最终决策权的负责人；
- 全程 Vibe Coding，AI 负责大部分样板代码、测试、重构和故障定位；
- 连续 3 个高投入开发日，每天约 10–12 小时；
- 需求和技术栈按 baseline 冻结；
- 已有可用 Linux 开发环境、Docker、域名/TLS 环境和 OpenCode；真实免费模型可用性属于外部依赖；
- 不在三天内追求代码优雅、通用抽象或生产 SLA。

MVP-0 预计为 **30–36 小时有效工程时间**。时间以验收结果为准，不按传统人日折算。

## 2. 开发原则

- 每 2–3 小时产出一个可运行 checkpoint；
- 先打通纵向链路，再补 UI 和错误处理；
- 每个关键模块至少有一个自动化测试；
- 任何新需求先进入 backlog，不在三天内临时扩范围；
- 不为了抽象复用推迟可运行闭环；
- 身份、归属校验、TLS、超时和 secret 日志红线不能作为赶工项删除。

## 3. Day 1：设备上线与配对

目标：

```text
Remote Client 启动
  → WSS/yamux 建连
  → Device challenge 认证
  → 显示 Pairing Code
  → WebUI Claim
  → Device List 显示 Online
```

### 0–2 小时：工程骨架

- 初始化 Go module 和 React/Vite；
- 建立 `cmd/`、`internal/`、`web/`、`migrations/`；
- 配置 Makefile、格式化、lint、单元测试命令；
- 定义 config、ID、错误结构和协议公共类型；
- 建立 SQLite migration runner。

Checkpoint：Server、Client、WebUI 都能独立启动，CI 命令可运行。

### 2–6 小时：Tunnel 和设备身份

- Client 生成/加载 Ed25519 Device Identity；
- `/api/v1/tunnel` WebSocket；
- WebSocket `net.Conn` + yamux；
- control stream header；
- challenge/signature/authenticated；
- Connection Manager；
- heartbeat、offline timer、重连 backoff；
- Tunnel/身份集成测试。

Checkpoint：Client 连接后 Server 日志显示已认证 Device，断网后状态切换正确。

### 6–9 小时：Auth、Pairing、Device API

- 单管理员 bootstrap；
- Login/Logout/Me；
- Session Cookie；
- Pairing Code 生成、HMAC digest、过期和一次性消费；
- Device List/Detail/Unpair；
- Owner 检查；
- SQLite WAL 和基础审计。

Checkpoint：curl/API 测试可完成登录、配对、二次配对失败。

### 9–12 小时：最小 WebUI

- Login 页面；
- Device List；
- Claim Pairing Code；
- Online/Offline badge；
- Device Detail 占位页；
- 前后端静态构建接通。

Day 1 验收：从全新 Client 到 WebUI 显示已绑定 Online Device，全链路通过。

## 4. Day 2：Embedded SSHD 与 Web Terminal

目标：

```text
Browser xterm.js
  → Terminal WebSocket
  → Server SSH Client
  → yamux ssh stream
  → Remote Embedded SSHD
  → PTY Shell
```

### 0–4 小时：SSH Server

- Remote Device Key 作为 SSH Host Key；
- 接收 `ssh` stream；
- 验证 Tunnel 临时 SSH Client Key；
- `session` channel；
- non-PTY `exec`；
- stdout/stderr/exit-status；
- SSH Host Key 严格验证；
- SSH exec 集成测试。

Checkpoint：Server 测试程序可以通过 Tunnel 执行 `printf`、`uname` 并读取退出码。

### 4–7 小时：PTY Shell

- `pty-req`；
- `shell`；
- stdin/stdout；
- `window-change`；
- 进程退出与 stream 清理；
- Remote Client 非 root 检查。

Checkpoint：本地测试客户端能运行 Shell、`stty size` 和一个交互程序。

### 7–10 小时：Terminal Gateway 与 UI

- Device Terminal WebSocket；
- 所有者与在线校验；
- binary terminal frames；
- text resize frames；
- xterm.js、fit addon；
- 页面退出后资源释放；
- 并发 Terminal 限制。

### 10–12 小时：异常路径

- Device Offline；
- Tunnel 中途断开；
- SSH 认证失败；
- Browser WebSocket 关闭；
- Terminal frame/resize 上限；
- 日志 request_id/connection_id/session_id。

Day 2 验收：浏览器可执行 Shell、交互程序和 resize；断开页面或 Tunnel 不残留会话。

## 5. Day 3：Agent 闭环、部署与 E2E

目标：

```text
User prompt
  → Agent streaming
  → remote_exec tool call
  → approval
  → Remote SSH exec
  → tool output
  → final answer
```

### 0–4 小时：Agent Loop

- Provider interface；
- OpenCode Session API、event SSE 与 Fake Adapter；
- `remote_exec` JSON Schema；
- Function Call → Tool Output → 继续生成；
- Turn 总时间、单命令时间和输出上限；正常 Agent 工具循环不设累计次数墙；
- Provider mock 单元测试。

Checkpoint：Mock Provider 能触发一次 Tool Call，SSH 执行后返回最终回答。

### 4–7 小时：审批与 Agent API

- Agent Session/Message/Tool Call 持久化；
- `per_command` 状态机；
- `approve_once`、`approve_session`、`deny`；
- Full Access 会话确认；
- SSE 内部事件格式；
- Device Offline 和用户取消；
- Agent/Tool 审计事件。

Checkpoint：API 测试能暂停待批准、批准后执行、拒绝后向模型返回拒绝结果。

### 7–9 小时：Agent Chat UI

- Session 创建；
- 消息输入和流式文本；
- Tool Call 卡片；
- Approve/Deny；
- 退出码、截断和失败展示；
- Turn running/waiting/completed 状态。

### 9–12 小时：部署、E2E 与修复

- 前端 build embed；
- Server/Client build；
- Dockerfile、Compose、Caddyfile；
- `.env.example`，不提交 secret；
- Playwright 主链路冒烟测试；
- 在独立 Remote 环境进行一次完整人工验收；
- 修复 P0/P1 问题；
- 记录已知问题和后续 backlog。

Day 3 验收：完成产品范围基线第 6 节的全部 12 项。

## 6. 测试矩阵

### 必须自动化

| 层级 | 测试 |
|---|---|
| Unit | ID、Pairing Code、HMAC、Device ID、协议长度上限 |
| Store | 配对事务、一次性消费、Session 过期、Device owner |
| Tunnel | challenge 成功/失败、heartbeat timeout、重复连接 newest wins |
| SSH | Host Key 校验、Client Key 校验、exec、退出码、PTY resize |
| Agent | Tool schema、审批状态、超时、截断、超过旧 12 次工具调用仍可继续 |
| HTTP | 未登录、非 owner、offline、Origin、统一错误格式 |
| E2E | 登录 → 配对 → 远程 exec/Terminal → Agent tool call |

### 必须人工验证

- Chrome/Chromium 的 Terminal 输入和 resize；
- 中文输入、ANSI color 和基本交互程序；
- Remote 网络短暂中断与恢复；
- Agent 审批信息是否足以让用户判断风险；
- 日志中没有 secret 和 Terminal 输入。

### 建议命令

```bash
go test ./...
go test -race ./internal/...
npm --prefix web test
npm --prefix web run build
npm --prefix web run e2e
docker compose -f deploy/compose.yaml config
```

## 7. 风险与预设降级

### R1：Embedded SSHD 的 PTY 兼容超时

首选不变：Embedded SSHD。

如果 Day 2 第 4 小时前 non-PTY exec 尚未跑通，立即启用预设降级：Remote Client 临时转发 `127.0.0.1:22` 的系统 OpenSSH。Terminal/Agent 仍走标准 SSH 和同一 Tunnel，不改变 Server/WebUI；MVP-0 演示环境需预装并配置 OpenSSH。Embedded SSHD 移到第一个修复项。

### R2：yamux over WebSocket 出现阻塞/关闭语义问题

先用 in-memory/net.Pipe 和本机 WebSocket 重现，最多投入 2 小时。仍无法解决则 MVP-0 临时改成每个 Terminal/Agent Session 独立 WSS 数据连接，control connection 保持不变。协议层保留 stream kind，Alpha 再恢复 multiplex。

### R3：OpenCode Tool Loop 或免费模型联调不稳定

Fake Adapter 必须先通过完整 Tool Call。真实 OpenCode 问题只允许占用 2 小时；外部限流时必须记录 `rate_limited`，不能伪造真实成功。Agent Tool Call 的本地/三端确定性闭环仍是 MVP 必须项，不能用纯聊天冒充完成。

### R4：前端消耗时间过多

删除动画、主题、响应式细节和组件美化，保留原生表单、设备列表、xterm.js 和 Tool Approval Card。功能验收优先。

## 8. 三天硬切线

三天期间禁止加入：

- 第二个 Agent Provider；
- PostgreSQL/Redis；
- 文件浏览器；
- 端口转发；
- Desktop；
- 多用户；
- Terminal reconnect；
- 安装器和自动更新；
- 移动端专项适配；
- 自研协议替代 SSH；
- 任何与 12 项验收无关的“顺手重构”。

## 9. MVP-0 交付物

- `aisummoner-server`；
- `aisummoner-client` Linux amd64 构建；
- 嵌入式 WebUI；
- SQLite migrations；
- Docker Compose + Caddy 示例；
- `.env.example`；
- 自动化测试与一条 E2E；
- `README` 启动说明；
- 已知问题清单；
- 人工验收记录。
