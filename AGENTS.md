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

`RemoteAgent_项目初步设计说明书.md` 是愿景和背景材料。发生冲突时，已接受 ADR 和 baseline 优先。

## 2. 当前任务边界

当前目标是三天完成 `MVP-0`：Linux-only、单管理员、单节点、自托管、小规模设备、功能闭环 Demo。

不得自行加入：

- Windows/macOS Client；
- 多用户/RBAC；
- PostgreSQL、Redis、消息队列或微服务；
- QUIC、P2P、Desktop；
- SFTP、文件 UI、端口转发；
- 绕过 ADR-0003 统一调用/展示适配层的第二个模型 Provider；
- OpenCode 之外的 CLI Agent、通用 MCP 或 Skills Runtime；
- Terminal reconnect；
- 与 12 项 MVP 验收无关的通用化重构。

发现有价值但不属于 MVP-0 的事项，记录到 backlog/issue，不在当前实现中顺手加入。

## 3. 架构不变量

- Remote Client 和 Server 使用 Go；WebUI 使用 React + TypeScript + Vite。
- Remote 只主动出站连接 Server，不监听远程控制 TCP 端口。
- Remote Transport 为 WSS，MVP 默认在其上运行 yamux。
- Terminal 和 Agent Remote Exec 都通过标准 SSH 语义实现。
- Remote Embedded SSHD 使用 Device Identity 作为 Host Key。
- Server 必须严格验证 Device/SSH Host Key，禁止 `InsecureIgnoreHostKey`。
- Agent Runtime 使用 loopback OpenCode sidecar；OpenCode 只能获得显式的远程工具，不能获得 Server 本地 shell。
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
- 前端以功能与清晰状态为先，不花时间做动画和主题系统。

## 6. 验证要求

每次实现应运行与变更最相关的最小测试；合入 MVP checkpoint 前运行：

```bash
go test ./...
go test -race ./internal/...
npm --prefix web test
npm --prefix web run build
```

涉及完整链路时，再运行 Playwright E2E 和 Docker Compose 配置检查。不能运行的测试必须在交付说明中明确写出原因，不能暗示已经通过。

必须覆盖失败路径：未认证、非 owner、Device Offline、超时、断线、输入过大、审批拒绝和 secret 脱敏。

## 7. 文档同步

- 协议、数据表、配置项或验收条件改变时，在同一个变更中更新 baseline。
- 核心技术选择或信任边界改变时新增 ADR。
- 不为普通重命名或内部重构创建 ADR。
- 代码实现与文档不一致时，不默认代码正确；先根据权威顺序判断并消除差异。
