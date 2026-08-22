# ADR-0002：MVP-0 使用 OpenCode 作为服务端 Agent Runtime

- 状态：Accepted
- 日期：2026-08-13
- 决策范围：AISummoner MVP-0 Agent 子系统
- 覆盖：ADR-0001 第 8、9 项及“直接包装通用 CLI Agent”相关结论

## 背景

MVP-0 需要验证服务端 Agent 经由 AISummoner SSH/Tunnel 操作指定 Remote。项目当前环境已经安装 OpenCode，并可看到 `opencode/*-free` 模型；用户要求先用 OpenCode 免费额度完成验证，避免额外配置模型 API Key。

实际探测确认 OpenCode 1.18.11 支持 headless server、Session API、SSE events、`run --format json`、自定义工具和权限控制。免费模型是外部共享服务，探测期间出现过 rate limit，因此不能作为确定性的自动测试依赖。

## 决策

1. MVP-0 的真实 Agent Provider 改为运行在 Server 同机的 OpenCode headless sidecar。
2. OpenCode 只监听 loopback，并使用随机 Basic Auth；不向公网暴露。
3. 每个 AISummoner Agent Session 对应一个 OpenCode Session 和独立空工作目录。
4. Go Server 通过 OpenCode HTTP Session API 和 `/event` SSE 接口创建会话、发送消息、取消 Turn 并映射事件。
5. OpenCode Workspace 内只启用 AISummoner 提供的 `remote_exec` 自定义工具；本地 Bash、文件读写、Web、子 Agent 和其他工具全部禁用。
6. `remote_exec` 通过 loopback bridge 回调 Go Server。Bridge 根据 OpenCode Session ID 映射到固定 AISummoner user/device/session，不接受模型选择任意主机。
7. 命令审批、超时、输出上限、并发限制和审计仍由 AISummoner Go Server 执行。
8. 自动测试使用行为等价的 Fake OpenCode Adapter；真实免费模型测试单独报告 `available`、`rate_limited` 或 `unavailable`，不得隐藏为通过。
9. OpenCode model 通过 `AISUMMONER_OPENCODE_MODEL` 配置；启动时探测能力，不把某个免费模型名称编译进代码。

## 目标链路

```text
Browser Agent Chat
  → AISummoner REST/SSE
  → Go Agent Orchestrator
  → OpenCode loopback HTTP/SSE
  → remote_exec custom tool
  → authenticated loopback bridge
  → approval gate
  → SSH over WSS/yamux
  → Remote Embedded SSHD
```

## 安全边界

- OpenCode 不是设备授权来源；Server 每次 Tool Call 都重新校验 Agent Session owner 和目标设备绑定。
- OpenCode 不获得 Server 本地 Shell。
- Bridge Token 只存在进程环境或内存，不写数据库、日志或前端。
- Tool 参数没有 `host`、`user`、`device_id`；目标设备由 Server 端 Session 映射决定。
- Full Access 只作用于当前 AISummoner Agent Session。
- Sidecar 退出、限流或协议未知时，Turn 明确失败，不回退到 Server 本地执行。

## 后果

正面：

- 使用现成 Agent Loop、上下文和免费模型入口；
- 无需在 AISummoner 内重写模型编排；
- OpenCode Session 可与产品 Session 一一对应；
- Fake Adapter 让核心链路测试不依赖公网模型稳定性。

负面：

- Server 部署增加 OpenCode/Bun runtime 依赖；
- OpenCode 事件 schema 和版本需要兼容测试；
- 免费额度可能限流，真实 Agent 验收存在外部波动；
- 自定义 TypeScript Tool 与 Go bridge 形成额外集成面。

## 被拒绝的备选方案

### 直接执行 `opencode run` 并开放 Bash

会让 Agent 在 Server 而不是 Remote 上执行命令，违反执行平面隔离。

### 仅用系统提示要求所有命令走 SSH

提示词不是权限边界，不能阻止模型调用本地工具。

### 三天内实现完整 OpenAI Responses Agent Loop

与用户指定 OpenCode 的目标冲突，也需要额外 API 配置。

### 让 OpenCode 运行在 Remote

违反 Server-side Agent 原则，并要求每个 Remote 安装和配置 Agent Runtime。

## 实际环境约束

- 本机 OpenCode：1.18.11，已有 OpenCode Go 凭据，免费模型探测曾被限流。
- ASD-Host OpenCode：1.17.8，尚无凭据；部署前需升级/固定版本并完成合法认证或确认匿名免费调用。
- lzr-host 不安装 OpenCode，只运行 Remote Client。
