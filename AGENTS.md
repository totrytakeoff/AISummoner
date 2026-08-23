# AISummoner Agent Instructions

本文件适用于整个 AISummoner 仓库。所有编码代理在规划、实现或评审前必须阅读并遵守。

## 1. 必读顺序

1. `README.md`
2. `docs/baseline/00-product-scope.md`
3. `docs/baseline/01-architecture-stack.md`
4. `docs/baseline/02-protocol-data-security.md`
5. `docs/baseline/03-mvp-plan.md`
6. `docs/decisions/ADR-0001-mvp-stack.md`
7. `docs/decisions/ADR-0002-opencode-runtime.md`
8. `docs/decisions/ADR-0003-agent-adapter-ui.md`
9. `docs/baseline/04-alpha-product-direction.md`
10. `docs/decisions/ADR-0004-alpha-clients-and-agent-runtime.md`

`RemoteAgent_项目初步设计说明书.md` 是愿景和背景材料。发生冲突时，已接受 ADR 和 baseline 优先。

## 2. 当前任务边界

`MVP-0` 的 Linux-only、单管理员、单节点功能闭环已经由真实链路验证。
当前进入 Alpha：保留既有安全/传输底座，按
`docs/baseline/04-alpha-product-direction.md` 重构 Controller、Remote Client GUI
和 Agent Runtime 兼容层。每次实现仍必须由当前 task plan 精确授权，不能把整个
Alpha 路线图一次性展开。

不得自行加入：

- Windows/macOS Client；
- 多用户/RBAC；
- PostgreSQL、Redis、消息队列或微服务；
- QUIC、P2P、Desktop；
- SFTP、文件 UI、端口转发；
- 绕过 ADR-0003/ADR-0004 统一 Runtime/Capability/展示适配层的 Provider；
- 未经独立 task plan 和 threat model 就启用 DSH/OpenCode/Codex/Claude 的
  Server 本地 shell、文件系统、通用 MCP 或 Skills；
- Terminal reconnect；
- 当前 task plan 之外的跨客户端通用化重构。

文件、桌面、多用户等后续能力只有在对应 task/ADR 明确授权时才能实现，不在
Controller 或 Adapter 重构中顺手加入。

## 3. 架构不变量

- Remote Client 和 Server 使用 Go；WebUI 使用 React + TypeScript + Vite。
- Remote 只主动出站连接 Server，不监听远程控制 TCP 端口。
- Remote Transport 为 WSS，MVP 默认在其上运行 yamux。
- Terminal 和 Agent Remote Exec 都通过标准 SSH 语义实现。
- Remote Embedded SSHD 使用 Device Identity 作为 Host Key。
- Server 必须严格验证 Device/SSH Host Key，禁止 `InsecureIgnoreHostKey`。
- Agent Runtime 使用 loopback OpenCode sidecar；OpenCode 只能获得显式的远程工具，不能获得 Server 本地 shell。
- DSH、OpenCode、Codex、Claude 等 Runtime 都只能通过 AISummoner Remote
  Capability Bridge 操作当前 owned Device；Browser 和 Runtime 均不是第二个
  Session/owner/approval 权威。
- Remote GUI 与 Go daemon 通过私有本地 IPC 分离；Remote 仍不监听可远程控制
  的 TCP 端口，GUI 不读取 Device 私钥。
- 持久化使用 SQLite WAL；在线连接只存在单节点内存 Connection Manager。
- 所有 Device、Terminal、Agent 操作都在 Server 端校验 owner。

改变上述任一项前必须新增或修改 ADR，并得到用户确认。

## 4. 安全红线

- 不删除 TLS、Device challenge、一次性配对、owner 校验、Agent 审批、超时或输出上限来赶进度。
- 不记录 Terminal 输入、密码、Cookie、私钥、Session Token 或 OpenAI API Key。
- 不把 secret 写入代码、测试 fixture、示例配置或 Git。
- Remote Client 默认拒绝 root 运行。
- 所有协议输入设置长度、数量、时间和并发上限。
- 不使用命令 denylist 宣称已经实现安全沙箱。
- Full Access 必须限定于一个 Agent Session，默认模式是逐命令审批。

## 5. 实现习惯

- 优先完成纵向可运行闭环，再做局部抽象。
- Go package 按 baseline 的业务边界组织，避免循环依赖。
- 网络与外部调用必须接收 `context.Context`，设置 deadline，并在关闭路径释放 goroutine/stream。
- 协议类型集中在 `internal/protocol`，所有消息显式带 version/type。
- 时间统一存储 UTC，API 使用 RFC 3339。
- ID 使用 `crypto/rand` 生成的不可预测带前缀 ID，不使用自增 ID 暴露资源规模。
- API 错误遵循 baseline 的统一 envelope，并携带 request ID。
- 新增依赖前说明必要性；优先标准库和已冻结技术栈。
- 前端以信息架构、状态清晰、可访问性和稳定布局为先；主题/动画必须服务交互，
  不得挤占当前 task 的核心状态与数据面验证。

## 6. 验证要求

每次实现应运行与变更最相关的最小测试；合入 Alpha task checkpoint 前运行：

```bash
go test ./...
go test -race ./internal/...
npm --prefix web test
npm --prefix web run build
```

涉及完整链路时，再运行 Playwright E2E 和 Docker Compose 配置检查。不能运行的测试必须在交付说明中明确写出原因，不能暗示已经通过。

必须覆盖失败路径：未认证、非 owner、Device Offline、超时、断线、输入过大、审批拒绝和 secret 脱敏。涉及工作区时还必须覆盖 Device/Session 快速切换、旧异步结果晚到、面板关闭与重挂载。

## 7. 文档同步

- 协议、数据表、配置项或验收条件改变时，在同一个变更中更新当前 Alpha baseline；MVP 验收文档作为历史记录只追加勘误，不改写既有证据。
- 核心技术选择或信任边界改变时新增 ADR。
- 不为普通重命名或内部重构创建 ADR。
- 代码实现与文档不一致时，不默认代码正确；先根据权威顺序判断并消除差异。
