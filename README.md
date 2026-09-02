# AISummoner

> A self-hosted remote AI workspace with a browser Controller, an outbound-only
> Remote Client, SSH Terminal, and approval-gated Agent execution.

AISummoner 是一个面向远程设备的自托管 AI Agent 与 SSH 控制平台。被控端只主动
向 Server 建立连接；用户在浏览器中完成设备配对、交互式 Terminal 和 Agent 会话，
所有模型工具调用最终都必须经过 AISummoner 的设备所有权、审批和 SSH 执行边界。

当前项目处于 **Alpha**。MVP-0 的端到端链路已经跑通，DSH-first Controller 和
Linux Qt Remote Client 已形成第一版可用形态；Windows 原生工程版也已完成 Core、Qt、
PowerShell/ConPTY 和 target-aware DSH 合约验证。接下来重点是完善 Agent 体验、接入
OpenCode/Codex/Claude Code，以及完成 Windows 10/11 与签名发布门槛。

[文档导航](docs/README.md) · [快速开始](docs/quick-start.md) ·
[开发路线](docs/roadmap.md) · [安全说明](SECURITY.md) ·
[参与开发](CONTRIBUTING.md) · [MIT License](LICENSE)

## 当前能力

| 模块 | 当前状态 |
| --- | --- |
| Browser Controller | DSH-first 三栏工作区；Device、Session、Agent、Terminal 与设置已打通 |
| Remote Client | Linux x86_64 AppImage；Windows x86_64 unsigned 工程 ZIP（Go Core + Qt GUI） |
| Terminal | Linux PTY 与 Windows PowerShell/ConPTY；交互、resize、关闭与断线回收 |
| Agent | DSH 多供应商/模型切换与 Linux/Windows 目标 Profile；统一 Remote 执行边界 |
| Session | 恢复、权限模式、归档、恢复、删除与有序事件重放 |
| 部署 | 单节点、单管理员、自托管；Docker/Caddy 示例与直接二进制部署 |
| 平台 | Linux 已验证；Windows Server 2022 工程 CI 已通过，Windows 10/11 尚未正式支持；macOS 尚未承诺 |

目前不是生产级远程管理产品：尚无多用户/RBAC、集群、文件管理、桌面控制，也没有
正式 Release 或稳定兼容承诺。公开仓库中的测试部署地址不构成公共服务 SLA。

## 产品流程

```text
启动 Remote Client
  → Remote 主动建立 WSS Tunnel
  → GUI 显示一次性配对码
  → Browser 登录并绑定 Device
  → 进入 Device Control Workspace
      ├─ 左侧：Agent Sessions
      ├─ 中间：Agent Conversation
      └─ 右侧：Terminal / Device 等可选组件
  → Agent 经审批后通过 SSH 在 Remote 执行命令
```

## 架构概览

```text
Browser Controller
  │ HTTPS / WSS / SSE
  ▼
Go Server
  ├─ Auth / Pairing / Device owner / Session / Approval
  ├─ DSH / OpenCode / DeepSeek / Fake adapters
  ├─ Remote Capability Bridge
  └─ strict SSH client
          │ WSS → yamux → SSH
          ▼
Remote Core daemon ── private local IPC ── Qt Desktop UI
  ├─ Device identity
  ├─ outbound reconnect
  └─ embedded SSHD / PTY / exec
```

核心原则：Server 是唯一的用户、Device、Session 和审批权威；Runtime 不能选择
设备，也不能借 Agent 工具访问 Server 本机 shell/filesystem。Remote Client 不开放
远程控制监听端口，GUI 不读取 Device 私钥。

详细设计见 [架构基线](docs/baseline/01-architecture-stack.md)、
[协议与安全基线](docs/baseline/02-protocol-data-security.md) 和
[Alpha 产品方向](docs/baseline/04-alpha-product-direction.md)。

## 快速开始

### 1. 本地 Fake Agent 开发

需要 Go 1.23+、Node.js 20.19+ 和 npm。

```bash
git clone https://github.com/totrytakeoff/AISummoner.git
cd AISummoner

install -m 600 .env.example .env
# 编辑 .env：设置管理员密码，并为 SESSION_SECRET、PAIRING_SECRET
# 分别生成至少 32 字节的独立随机值。

npm --prefix web ci
```

完整的 Server/Web 双进程启动方式见
[快速开始：本地开发](docs/quick-start.md#本地-fake-agent-开发)。

### 2. Linux Remote Client

桌面客户端交付物是 `AISummoner-Remote-0.1.0-x86_64.AppImage`，其中同时包含
Qt GUI 与 Go daemon。它与旧的 CLI-only
`AISummoner-Client-0.1.0-x86_64.AppImage` 不是同一个产物。

```bash
chmod +x AISummoner-Remote-0.1.0-x86_64.AppImage
./AISummoner-Remote-0.1.0-x86_64.AppImage

# 目标系统没有 FUSE 时：
APPIMAGE_EXTRACT_AND_RUN=1 ./AISummoner-Remote-0.1.0-x86_64.AppImage
```

必须由普通桌面用户运行。GUI 会自动启动同包 daemon；默认 Server Origin 在构建期
写入，普通用户无需填写地址。自托管构建可通过
`AISUMMONER_DEFAULT_SERVER_ORIGIN=https://control.example.com` 覆盖。

当前仓库尚未发布 GitHub Release；请按
[Remote AppImage 构建说明](docs/quick-start.md#构建-linux-remote-appimage)自行构建，
不要从来源不明的文件分享渠道获取客户端。

### 3. 自托管部署

仓库提供 Docker/Caddy 示例，以及 DSH 私有 Runtime 的直接部署路径。先阅读
[部署指南](docs/deployment.md)，特别是 TLS、精确 trusted proxy、私有数据目录、
Provider secret 与公开端口约束。

```bash
install -m 600 .env.example .env
# 编辑 .env 后先确认权限，再运行 Compose 的 secret-safe 校验。
test "$(stat -c '%a' .env)" = 600
sh deploy/validate-compose.sh
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

## Agent Runtime 路线

- **[DSH（DeepSeek Harness）](https://github.com/deepseek-ai/deepseek-harness)**：
  当前一等 Runtime/UX 基线，真实 Remote 工具链已跑通；支持 DSH 原生供应商配置、
  自定义兼容网关，以及当前会话的模型/推理强度切换。
- **OpenCode**：已有 MVP 适配，下一阶段补齐原生 Session/Turn/cancel/usage 映射。
- **Codex**：计划通过官方 App Server 机器接口接入。
- **Claude Code**：计划通过官方 Agent SDK/流式接口接入。
- **Direct DeepSeek**：保留为轻量模型 API 兼容层。
- **Fake**：仅用于确定性测试与离线开发。

Runtime 适配顺序与平台计划见 [公开 Roadmap](docs/roadmap.md)。

## 验证

```bash
GOMAXPROCS=2 go test -p 2 ./...
GOMAXPROCS=2 go test -p 2 -race ./internal/...
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Qt、AppImage、Compose 和真实三机验收命令见
[开发贡献指南](CONTRIBUTING.md)与 [验收记录](docs/acceptance/)。重型 Go race、
Node build 和 Docker build 应串行执行，避免 OOM。

## 安全边界

- Remote 只主动出站，生产连接要求标准 TLS/WSS。
- Device challenge、一次性配对、owner predicate 与严格 SSH Host Key 验证不可绕过。
- Agent 默认逐命令审批；Full Access 仅作用于当前 Agent Session。
- 密码、Cookie、API Key、Device 私钥、Terminal 输入和 Agent 命令/输出不得写日志。
- DSH/OpenCode/Codex/Claude 只能通过 Device-bound Remote Capability 工作。

安全问题请不要直接公开细节，参见 [SECURITY.md](SECURITY.md)。

## 文档与决策

- [完整文档地图](docs/README.md)
- [产品与范围基线](docs/baseline/00-product-scope.md)
- [Alpha 产品方向](docs/baseline/04-alpha-product-direction.md)
- [架构决策记录](docs/decisions/)
- [设计文档](docs/design/)
- [MVP/部署验收证据](docs/acceptance/)
- [AI 协作与任务交接](docs/agent_context/)

发生冲突时，以已接受 ADR 和当前 baseline 为准；README 是项目入口，不替代安全
基线。早期愿景保留在
[《RemoteAgent 项目初步设计说明书》](RemoteAgent_项目初步设计说明书.md)。

## License

AISummoner 采用 [MIT License](LICENSE)。第三方组件及 DSH 体验移植仍保留各自的
版权与许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
