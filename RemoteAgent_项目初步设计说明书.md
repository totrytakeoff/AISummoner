# RemoteAgent 项目初步设计说明书

> 本文用于记录 RemoteAgent 项目的初步需求、架构设计与开发边界，作为后续实现与迭代的基础设计备忘录。

## 1. 项目背景与动机

目前主流远程控制软件大多以“远程桌面”为中心，核心交互方式是屏幕画面、鼠标和键盘转发。这种模型适合人工操作 GUI，但在 AI Agent / CLI 工具已经能直接分析日志、执行命令、修改代码和调试系统的场景下，桌面像素流反而成为低效的中间层。

本项目希望构建一种 **CLI / Agent First 的远程控制系统**：

- 被控端启动一个轻量客户端即可接入系统；
- 控制端只需要浏览器即可完成设备配对、终端操作和 Agent 对话；
- Agent 工具链统一运行在服务端，不依赖控制端或被控端的本地环境；
- 被控端提供标准 SSH 能力，服务端通过反向连接访问其终端与系统资源；
- GUI 桌面控制作为辅助能力，而非主控制平面。

最终希望达到的效果是：

> 浏览器打开任何一台已接入的机器，把云端 Agent 接上去。

---

## 2. 核心产品定位

项目不是传统意义上的“远程桌面”，也不只是一个内网穿透工具，而是一个：

**Remote Agent Runtime / Agent-native Remote Access Platform**

核心抽象为：

```text
Control Device = UI
Server         = Intelligence + Control
Remote Device  = Execution
```

即：

```text
Presentation Plane
       ↓
Intelligence / Control Plane
       ↓
Execution Plane
```

三者相互解耦。

---

## 3. 总体架构

系统由三部分组成：

1. 控制端客户端：WebUI
2. 服务端：控制平面 + Agent Runtime
3. 被控端客户端：常驻远程控制 Agent / Daemon

总体结构：

```text
┌────────────────────────────┐
│        Control Client      │
│            WebUI           │
│                            │
│  Terminal / Agent Chat     │
│  Device List / File / GUI  │
└──────────────┬─────────────┘
               │ HTTPS / WebSocket
               ▼
┌────────────────────────────────────┐
│               Server               │
│                                    │
│  Auth / Device Registry            │
│  Pairing / Session Management      │
│  Tunnel Gateway                    │
│  Terminal Gateway                  │
│  Agent Runtime Manager             │
│  Agent Toolchain / MCP / Skills    │
│  Audit / Permission / Secrets      │
└────────────────┬───────────────────┘
                 │ Persistent Reverse Tunnel
                 ▼
┌────────────────────────────────────┐
│          Remote Client             │
│                                    │
│  Persistent Connection             │
│  Embedded / Managed SSHD           │
│  PTY / Shell / Filesystem          │
│  Device Information                │
│  Optional Desktop Control          │
└────────────────────────────────────┘
```

---

## 4. 控制端 WebUI

控制端尽量保持无状态、轻量化。

用户原则上不需要安装任何本地客户端，只需要浏览器访问服务端。

核心功能：

- 登录 / 用户身份认证；
- 输入配对码绑定新设备；
- 查看已绑定设备及在线状态；
- 打开 Web Terminal；
- 与服务端 Agent 进行对话；
- 查看 Agent 执行的命令、输出与操作状态；
- 后续扩展文件管理、端口转发、桌面远控等能力。

Web Terminal 可采用：

```text
xterm.js
    ↓ WebSocket
Server Terminal Gateway
    ↓ SSH Channel
Remote SSHD
```

因此浏览器只负责展示终端，本身不需要实现远程 Shell 逻辑。

---

## 5. 服务端

服务端是整个系统的核心。

与传统远控相比，本项目最关键的设计是：

> Agent 不属于控制端，也不属于被控端，而属于 Server。

因此服务端同时承担：

### 5.1 Control Plane

负责：

- 用户认证；
- 设备注册；
- 设备配对；
- 在线状态维护；
- 会话管理；
- 权限控制；
- 操作审计；
- 密钥 / 凭证管理。

### 5.2 Tunnel Gateway

负责维护 Remote Client 主动建立的长连接，并在此之上承载多个逻辑通道。

逻辑上可抽象为：

```text
Remote Client
      │
      │ Persistent Connection
      ▼
Tunnel Gateway
      │
      ├── Control Channel
      ├── SSH Channel #1
      ├── SSH Channel #2
      ├── File Channel
      └── Desktop Channel
```

初版可基于：

- TCP；
- WebSocket；

后续再考虑：

- QUIC；
- yamux；
- smux；
- 其他成熟 multiplex 方案。

不建议自行长期维护完整多路复用协议。

### 5.3 Agent Runtime

Agent Runtime 由服务端统一管理。

可运行：

- Codex；
- Claude Code；
- OpenCode；
- 自研 Agent；
- MCP Servers；
- Skills；
- Git / Python / Shell 等辅助工具。

Agent 通过标准 SSH 访问被控机器，而无需适配 RemoteAgent 自身协议。

理想模型：

```text
Agent Runtime
    │
    │ ssh target-device
    ▼
Server Tunnel Gateway
    │
    ▼
Remote Client
    │
    ▼
Remote SSHD
```

这样任何现有 CLI Agent 都可以直接复用。

---

## 6. 被控端客户端

被控端目标是尽量轻量、开箱即用。

启动后应完成：

1. 初始化设备身份；
2. 连接 Server；
3. 注册设备；
4. 生成临时配对码；
5. 维护持久反向连接；
6. 向服务端暴露 SSH 能力；
7. 处理设备信息、心跳及后续扩展能力。

示例：

```bash
remote-agent start
```

输出：

```text
RemoteAgent Client

Server:
    remote.example.com

Pairing Code:
    K7HF-92PQ

Expires in 10 minutes.

Waiting for controller...
```

被控端无需公网 IP，也不要求端口转发。

所有网络连接默认由被控端主动向服务端发起：

```text
Remote Client ─────► Server
```

从而天然解决绝大多数 NAT 场景。

---

## 7. SSH First 设计

SSH 是项目的重要基础抽象。

不计划自行重新设计一套完整的远程 Shell 协议。

原因在于 SSH 已经解决：

- Shell；
- PTY；
- stdin / stdout / stderr；
- Signal；
- Terminal Resize；
- SFTP；
- SCP；
- Port Forwarding；
- Interactive Program；
- tmux / vim / gdb / htop 等终端程序兼容性。

如果自己设计：

```text
EXEC command
READ file
WRITE file
PTY resize
SIGNAL
...
```

最终很容易变成重新实现 SSH。

因此核心原则是：

> 不让 Agent 适配 RemoteAgent Protocol，而让 RemoteAgent 为 Agent 暴露标准 SSH。

---

## 8. SSHD 设计

当前倾向于由 Remote Client 内嵌或管理一个独立 SSHD。

逻辑结构：

```text
Server SSH Client
       │
       ▼
Reverse Tunnel
       │
       ▼
Remote Client
       │
       ▼
127.0.0.1:SSHD
```

SSHD 不需要监听公网端口。

它甚至可以只绑定 localhost，由 Remote Client 的隧道逻辑进行转发。

具体实现可后续评估：

- OpenSSH sshd 子进程；
- libssh Server；
- Rust SSH Server Library；
- Go SSH Server；
- 平台原生 SSH 服务复用。

初版优先选择成熟方案，避免自行实现 SSH 协议。

---

## 9. 设备身份与配对机制

用户所看到的“密钥”应当定义为 **Pairing Code**，而非长期身份私钥。

至少区分以下三类凭证：

### 9.1 Device Identity Key

客户端首次启动生成长期设备密钥：

```text
Ed25519 Key Pair
```

私钥仅存储于被控端，用于向服务端证明：

```text
I am device-xxx
```

该私钥绝不能展示给用户。

### 9.2 Pairing Code

例如：

```text
K7HF-92PQ
```

特点：

- 用户可见；
- 一次性；
- 短期有效；
- 只用于将 device_id 与 user_id 建立绑定。

流程：

```text
Remote Client
    │
    ├── device_id
    └── pairing_code
             │
             ▼
           Server

WebUI 输入 pairing_code
             │
             ▼
Server: bind(user_id, device_id)
```

绑定成功后 Pairing Code 立即失效。

### 9.3 Session Credential

每次 Terminal 或 Agent 访问 Remote 时，再由服务器签发会话级凭证。

后续可考虑：

- 临时 SSH Key；
- OpenSSH Certificate；
- 短期 Token。

例如：

```text
valid: 10 min
principal: remote-agent
session: xxx
```

避免长期凭证在多个 Agent Runtime 中扩散。

---

## 10. Terminal 控制数据流

```text
Browser
   │
   │ keyboard / terminal data
   ▼
xterm.js
   │
   │ WebSocket
   ▼
Terminal Gateway
   │
   │ SSH Client
   ▼
Tunnel Gateway
   │
   ▼
Remote Client
   │
   ▼
Remote SSHD
   │
   ▼
Shell / PTY
```

输出反向返回浏览器。

用户最终获得完整交互式终端体验。

---

## 11. Agent 数据流

用户输入：

```text
帮我看看 nginx 为什么启动失败
```

数据流：

```text
Browser Agent Chat
      │
      ▼
Server Agent Runtime
      │
      │ SSH
      ▼
Remote Machine
```

Agent 可以执行：

```bash
systemctl status nginx --no-pager
journalctl -u nginx -n 200 --no-pager
nginx -t
```

然后继续进行分析与修复。

关键点：

Agent 本身无需部署在 Remote Client 上。

Agent 的：

- Context；
- MCP；
- Skills；
- API Key；
- Conversation；
- Workspace；

全部由 Server 维护。

---

## 12. Agent Runtime 隔离

不建议所有 Agent 直接运行在主服务进程环境中。

后续推荐：

```text
Agent Runtime Manager
       │
       ├── Container A
       ├── Container B
       └── Container C
```

每个 Agent Session / Workspace 使用独立容器：

```text
Agent Container
├── codex / claude / opencode
├── ssh
├── git
├── python
├── MCP
├── Skills
├── workspace
└── temporary credentials
```

好处：

- 用户隔离；
- Agent 隔离；
- 凭证隔离；
- 文件系统隔离；
- 更容易限制 CPU / RAM / 网络；
- Agent 崩溃不影响 Server 主进程。

第一版 MVP 可不立即实现完整容器隔离，但架构上应预留。

---

## 13. 安全模型

Agent 拥有远程执行能力，因此不能只采用传统“登录成功即完全信任”的模型。

长期目标应支持 Agent Capability Policy。

例如：

```yaml
agent:
  filesystem:
    read:
      - /var/log/**
      - /home/user/project/**

    write:
      - /home/user/project/**

  command:
    allow:
      - git
      - cmake
      - ninja
      - journalctl
      - systemctl status

    confirm:
      - apt install
      - systemctl restart

    deny:
      - shutdown
      - reboot
      - mkfs
```

当 Agent 请求高风险操作：

```text
systemctl restart nginx
```

WebUI 可显示：

```text
Privileged operation requested

Agent: Codex
Device: workstation
Command:
    systemctl restart nginx

[Approve Once]
[Approve Session]
[Deny]
```

该权限系统属于重要演进方向，但不是首版 MVP 的必要条件。

---

## 14. 网络设计原则

本项目的第一目标不是创建 Tailscale 类 Overlay Network。

由于系统天然存在中心 Server，因此第一版直接采用 Relay 架构：

```text
Remote Client
     │
     ▼
Server
     │
     ▼
Controller / Agent
```

优点：

- 简单；
- NAT 友好；
- 易于统一认证；
- 易于审计；
- Agent 本身就在 Server；
- 不需要解决 Controller ↔ Remote 的直接 P2P。

后续当带宽需求增加，例如桌面视频流，可再增加：

```text
Controller ◄──── P2P ────► Remote
```

Server 只负责 signaling / rendezvous。

因此项目网络能力可以分阶段演进，而无需第一版就实现完整 P2P NAT Traversal。

---

## 15. 桌面远控定位

桌面远控是辅助能力，不是核心能力。

优先级：

```text
SSH / CLI / Agent
        >
File / Port Forward
        >
Desktop Control
```

桌面控制主要用于：

- GUI 软件；
- 浏览器状态；
- 图形化 IDE；
- 系统设置界面；
- CLI 无法覆盖的场景；
- 人工辅助观察 Agent 操作结果。

第一版可以完全没有桌面功能。

---

## 16. MVP 范围

第一阶段只验证最核心闭环：

### Remote Client

- 启动；
- 注册设备；
- 生成 Pairing Code；
- 建立 Server 长连接；
- 维护心跳；
- 提供 SSHD；
- Tunnel TCP / SSH 数据。

### Server

- 用户基础认证；
- Device Registry；
- Pairing；
- Remote Client Connection Manager；
- Tunnel Gateway；
- SSH Client；
- WebSocket Terminal Gateway；
- Agent Runtime 基础接入。

### WebUI

- 输入 Pairing Code；
- Device List；
- Online / Offline 状态；
- Web Terminal；
- Agent Chat。

MVP 完成标准：

```text
Remote PC
    ↓ start remote-client
获得 Pairing Code
    ↓
浏览器输入 Pairing Code
    ↓
设备出现在 Device List
    ↓
Open Terminal
    ↓
获得 Remote Shell
    ↓
Open Agent Chat
    ↓
Agent 可以通过 SSH 操作 Remote PC
```

这一闭环成立即认为第一阶段成功。

---

## 17. 推荐开发阶段

### Phase 0：Tunnel PoC

只实现：

```text
Remote Client ─ TCP/WebSocket ─ Server
```

Server 能通过 Remote Client 转发访问：

```text
127.0.0.1:22
```

验证 SSH 是否工作。

### Phase 1：Device / Pairing

实现：

- Device ID；
- Device Key；
- Pairing Code；
- Device Registry；
- 用户设备绑定。

### Phase 2：Web Terminal

加入：

- xterm.js；
- WebSocket Terminal；
- SSH PTY；
- resize；
- reconnect。

### Phase 3：Agent Runtime

加入服务端 Agent：

```text
Chat
 ↓
Agent
 ↓
SSH
 ↓
Remote
```

优先支持一个 Agent 工具即可。

### Phase 4：Runtime Isolation

加入：

- Container；
- Workspace；
- Session Credential；
- Secret Manager。

### Phase 5：File / Port Forward

利用 SSH 能力增加：

- SFTP；
- Upload / Download；
- TCP Forwarding；
- Web Service Preview。

### Phase 6：Desktop

最后增加：

- Screen Capture；
- Video Encoding；
- Mouse / Keyboard；
- Clipboard；
- P2P Media Channel。

---

## 18. 当前已经明确的设计原则

### 原则 1：Agent Server-side

Agent 工具链由 Server 维护。

控制端与被控端都不负责 Agent 环境。

### 原则 2：Remote 主动连接 Server

避免公网 IP、端口转发和复杂 NAT 配置。

### 原则 3：SSH First

CLI 控制使用标准 SSH，而不是重新实现 Shell Protocol。

### 原则 4：Web First Controller

控制端只依赖浏览器即可完成主要操作。

### 原则 5：CLI / Agent First，Desktop Second

项目首先解决 Agent 与 Terminal 控制问题。

### 原则 6：标准生态优先

尽量复用：

- OpenSSH；
- xterm.js；
- WebSocket / QUIC；
- Container Runtime；
- 现有 Agent CLI。

避免无必要地重新造轮子。

### 原则 7：Server 是可信控制平面

首版不追求 Zero-Knowledge / 完全端到端不可见。

Server 本身承担 Agent 执行与安全控制职责，因此属于系统信任边界的一部分。

---

## 19. 尚未定案的问题

以下属于后续技术选型，不应在当前阶段过早锁死。

### Remote Client 语言

候选：

- Rust；
- Go；
- C++。

### Server 语言

候选：

- Go；
- Rust；
- C++；
- 混合架构。

### Tunnel Transport

第一版：

```text
TCP / WebSocket
```

后续：

```text
QUIC
```

### Multiplex

候选：

- QUIC Streams；
- yamux；
- smux；
- 自定义极简协议（仅 PoC）。

### SSHD

候选：

- OpenSSH；
- libssh；
- 语言生态中的 SSH Server Library。

### Agent Runtime

第一阶段可直接调用已有 CLI Agent。

后续再决定是否构建统一 Agent Adapter Layer。

### Desktop Protocol

暂不确定，可后续研究：

- RustDesk 方案；
- WebRTC；
- PipeWire + Hardware Encoder；
- RDP / VNC Bridge。

---

## 20. 非目标 / 避免过度设计

第一阶段明确不需要：

- 完整 Tailscale 替代品；
- 自研 WireGuard；
- 自研 SSH；
- 完整 NAT Traversal；
- P2P Desktop；
- 企业级 RBAC；
- 完整审计录像；
- 多 Agent 编排平台；
- Kubernetes；
- 大规模分布式 Server Cluster。

开发过程应始终优先验证：

> “Agent 是否能够方便、稳定、安全地通过 Server 控制 Remote Device？”

而不是提前建设外围基础设施。

---

## 21. 项目最终愿景

传统远控：

```text
Human
  ↓
Remote Screen
  ↓
Mouse / Keyboard
  ↓
Remote Machine
```

RemoteAgent：

```text
Human
  ↓
Web UI
  ↓
Cloud Agent / Terminal
  ↓
SSH
  ↓
Remote Machine
```

GUI 退化为辅助观察通道。

最终希望将远程计算机从：

> Remote Screen

重新抽象为：

> Remote Execution Environment

使用户能够从任何设备、任何浏览器中，将统一的云端 Agent 工具链接入任意已授权设备，并持续完成开发、分析、排障和运维工作。

---

## 22. 一句话定义

> **RemoteAgent 是一个以 Server-side AI Agent 和 SSH 为核心的远程执行平台：被控端只需启动客户端接入服务器，控制端通过浏览器即可获得终端与 Agent 控制能力。**
