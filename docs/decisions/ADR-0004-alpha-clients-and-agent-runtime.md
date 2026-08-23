# ADR-0004：Alpha 双客户端与 Agent Runtime 架构

- 状态：Accepted
- 日期：2026-08-23
- 决策范围：MVP-0 后的 Controller、Remote Client 与多 Agent Runtime
- 关联基线：`docs/baseline/04-alpha-product-direction.md`

## 背景

MVP-0 和真实 DeepSeek 测试证明了 AISummoner 的登录、配对、Tunnel、SSH、Terminal、审批和 Agent Remote Exec 纵向链路。当前产品界面和运行时抽象仍以验证链路为目的：Browser 按 Device/Terminal/Agent 分页；Remote Client 只有 CLI；`agent.Adapter.Run` 只覆盖一次 Turn 的最小共同面。

用户确认后续必须同时重构 Controller 与 Remote Client。Controller 以 DSH 的 Agent 交互为首要参考，采用类似 VS Code/Zed 的工作区布局；Agent Runtime 按 DSH、OpenCode、Codex、Claude Code 的顺序兼容；Remote Client 提供 GUI，但继续保留非 root、只出站的守护核心。

## 决策

### 1. Controller 改为以设备为作用域的 Control Workspace

一级流程固定为 `登录 → 选择/绑定设备 → Control Workspace`。工作区采用三分区：左侧 Device/Session rail，中间 Agent Conversation，右侧可选 Terminal/Device Activity/未来 Desktop dock。面板支持折叠、调整大小和工作区内最大化；移动端降级为单主面板加抽屉/tab。

Terminal 与 Agent 不再是互斥的整页入口。工作区必须在设备切换时原子取消旧设备的 SSE、WebSocket、请求和本地投影，避免跨设备状态污染。

### 2. 借鉴 DSH 交互合同，不引入其产品权威

采用 DSH 的有序 Session 事件、三栏布局、Session 导航、reasoning 分离、tool renderer、审批接管 composer 和 capability/provider 分层思路。AISummoner 不采用 Cordis/DSH Host 作为主后端，不复制其 credential/session store，不开放 DSH 本地 shell/filesystem，也不把 DSH Web 作为长期 iframe/fork。

### 3. AISummoner Session Log 保持唯一事实来源

所有 Adapter 的原生事件先由 Server 归一化并持久化。Browser 不解析 Provider wire schema。Runtime 原生 Session/Thread ID 只作为 owner-scoped 恢复 metadata；owner、Device、approval、tool execution 和公共历史仍由 AISummoner 控制。

### 4. Agent 抽象分为 Runtime、Capability 与 Presentation

- Runtime Adapter 管理原生 Session/Turn、stream、cancel、steer、approval/question 和 health/model 信息。
- Capability Descriptor 声明 Runtime 实际支持的动作，Controller 按能力渐进增强。
- Remote Capability Bridge 把 shell、文件、patch、Terminal 和未来 Desktop 映射到当前 owned Device。
- Provider/Tool Presentation Adapter 只负责通用事件的展示。

任何 Adapter 都不得选择 Device、绕过审批或获得 Server 本地执行权。

### 5. Runtime 实施顺序固定

丰富 Agent Runtime 的实施优先级为：

```text
DSH → OpenCode → Codex → Claude Code
```

直接 DeepSeek 保留为轻量模型 API Adapter。DSH 首先验证标准事件 v2 与交互；OpenCode 升级现有集成；Codex 优先使用官方 App Server 的 stdio/Unix socket 协议；Claude Code 使用官方 Agent SDK streaming input。不得以解析 CLI/TUI 文本代替稳定协议。

### 6. Remote Client 采用 Core Daemon + Desktop UI

现有 Go Tunnel/SSHD 成为独立 daemon。桌面 UI 使用原生 Qt 6 Widgets/C++，
通过 mode `0600`、校验 peer UID 的 Unix socket 读取状态并发送本地操作；UI
不读取 Device 私钥，不新增公网/局域网监听。首阶段不引入 QML/WebEngine，Qt
只依赖 Core/Gui/Widgets/Network。

GUI 第一阶段必须支持：查看/刷新配对码、连接和被控状态、脱敏活动记录、Disconnect/Pause、Resume。关闭 UI 默认不停止 daemon；手动 Disconnect 会 joined 关闭当前 Tunnel/子会话并暂停重连，但保留 identity 与配对。Unpair/Reset identity 是独立危险动作。未来本机权限策略只能进一步拒绝 Server 已授权能力，不能扩大权限。

### 7. 保留所有既有安全不变量

TLS、Device challenge、一次性配对、owner predicate、严格 SSH Host Key、Session 审批、协议/字节/时间/并发上限、secret redaction 和 joined shutdown 不因 UI/Adapter 重构而放宽。所有 sidecar 默认隔离且 deny Server-local tools。

## 后果

正面：

- 两个客户端有清晰、可分别演进的产品边界；
- 新 Agent Runtime 复用同一会话 UI 和安全执行面；
- Controller 可以同时使用 Agent、Terminal 和未来桌面组件；
- Remote GUI 不会把桌面生命周期耦合进可靠的 tunnel daemon。

代价：

- 需要新增标准事件 v2、Capability Descriptor、Session 列表和 Provider Profile；
- Remote Client 需要本地 IPC、GUI toolkit 与双交付模式；
- DSH/OpenCode/Codex/Claude 的恢复、授权和工具模型不能只靠一个最低共同接口，需要逐 Runtime contract tests；
- 迁移期需要兼容现有 Session/event 数据并保留旧路由跳转。

## 被拒绝的备选方案

### 直接把 DSH Web/Backend 作为 AISummoner Agent 模块

这会形成第二个 Session、credential、工具和本地执行权威，与 AISummoner 的 Device owner/Tunnel/SSH 边界冲突，也会把 pre-1.0 DSH 内部结构变成产品依赖。

### 每个 Runtime 独立开发页面

这会复制消息、reasoning、tool、approval、error 和 Session 交互，使 Provider 行为长期漂移。

### Remote GUI 与 Tunnel Core 合成单一窗口进程

窗口关闭、桌面崩溃或 WebView 更新会意外断开 Remote；同时不利于 headless/systemd 部署和权限隔离。

### 用 CLI/TUI 输出适配 Codex 或 Claude Code

终端输出不是稳定协议，难以可靠表达 delta、approval、tool result、cancel 和 resume，也容易泄露 ANSI/内部文本。必须使用官方机器接口。
