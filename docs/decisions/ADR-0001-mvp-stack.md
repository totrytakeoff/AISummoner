# ADR-0001：MVP-0 总体架构与技术栈

- 状态：Accepted
- 日期：2026-08-12
- 决策范围：AISummoner MVP-0

> Agent Runtime 相关第 8、9 项及 CLI Agent 结论已由 [ADR-0002](ADR-0002-opencode-runtime.md) 覆盖；其他决策继续有效。

## 背景

AISummoner 需要在三天连续 Vibe Coding 内验证“浏览器把服务端 Agent/Terminal 接入出站联网 Linux 设备”的核心闭环。项目愿景很大，但 MVP 的主要风险集中在反向连接、PTY/SSH、设备身份和 Agent 远程执行语义。

## 决策

1. Remote Client 和 Server 统一使用 Go。
2. Server 为单体进程，WebUI 构建后嵌入 Server。
3. WebUI 使用 React、TypeScript、Vite、xterm.js。
4. Remote 长连接使用 WSS，连接上使用 yamux 承载 control 和多个 SSH stream。
5. Remote 在 stream 上提供 Embedded SSHD，不监听 TCP 端口。
6. Server 通过标准 SSH 同时实现 Terminal 和 Agent Tool Call。
7. MVP 数据存储使用 SQLite WAL，不引入外部数据库和缓存。
8. Agent 使用 OpenAI Responses API、function calling 和流式输出；模型通过配置选择。
9. Agent 只获得结构化 `remote_exec`，不获得 Server 本地 shell。
10. MVP-0 仅支持 Linux、单管理员、单节点和少量设备。
11. Docker Compose + Caddy 作为示例部署方式。

## 原因

- Go 能以较少语言和运行时复杂度覆盖 Client、Server、SSH 和网络并发。
- SSH 已经解决 Shell、PTY、resize、signal 和 exec 语义，避免重新设计远程 Shell 协议。
- WSS 对代理、防火墙和自托管部署友好；yamux 可以复用一个主动连接。
- SQLite 对单节点 MVP 足够，减少部署和 Vibe Coding 联调成本。
- 结构化 Agent Tool 能明确“命令运行在 Remote”，避免通用 CLI Agent 混淆 Server 与 Remote 文件系统。
- 单体架构有利于三天内完成纵向闭环，内部 package 边界为后续拆分保留空间。

## 后果

正面：

- 首版依赖和部署拓扑小；
- Client 可发布为单二进制；
- Terminal 与 Agent 复用同一 SSH 能力；
- 设备主动连接天然适应大多数 NAT 环境；
- Agent 命令容易审计、限时和审批。

负面：

- Server 是可信控制平面，能看到命令和输出；
- 单节点 Connection Manager 无法水平扩展；
- SQLite 不适合高写入并发和多副本；
- Embedded SSHD 需要自行正确处理 SSH channel/PTY 边界；
- MVP Agent 不是完整 CLI Coding Agent；
- SSE 不提供 MVP-0 重启后的事件恢复。

## 被拒绝的备选方案

### Rust/C++ Client

长期可能有价值，但首版会增加构建、异步生态或内存安全成本，不利于三天闭环。

### QUIC/P2P/WireGuard

不是验证产品价值的必要条件；中心 Server 已存在，Relay 更简单。

### 自研 Shell/Exec 协议

会逐步重新实现 PTY、signal、resize、interactive program 和文件/端口能力，因此拒绝。

### 直接包装通用 CLI Agent

CLI Agent 默认把自身工作目录和 shell 当作执行环境，容易误操作 Server。MVP 先使用结构化 Remote Tool；CLI Adapter 留到 Alpha。

### PostgreSQL、Redis、微服务

对单管理员、少量设备的三天 Demo 没有收益，只增加部署和故障面。

### Remote 依赖系统 OpenSSH

可作为 Day 2 风险降级，但不是默认方案。默认 Embedded SSHD 才能保持单二进制和无主机配置。

## 复审触发条件

出现以下任一情况时新增 ADR，不直接静默修改本决策：

- 支持第二种 Remote OS；
- 支持公开多用户服务；
- 单 Server 达到 1000 个同时在线设备；
- 需要 Server 水平扩容；
- 引入 CLI Agent/MCP/Skills Runtime；
- 引入 Desktop 或高带宽数据通道；
- SQLite、WSS 或 yamux 经测量成为瓶颈；
- 信任模型转向端到端加密或 Zero-Knowledge。
