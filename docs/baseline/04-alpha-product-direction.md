# Alpha 产品与双客户端重构方向

- 状态：Accepted Direction
- 日期：2026-08-23
- 适用阶段：MVP-0 之后的 Alpha 开发
- 决策来源：用户确认的产品方向与 MVP 实测反馈

## 1. 定位

MVP-0 已经证明下列纵向链路能够工作：

```text
Browser 登录/配对
  → Server owner 与审批边界
  → WSS/yamux Tunnel
  → 严格 SSH Host Key 验证
  → Remote exec / PTY
  → Terminal 或 Agent 结果回到 Browser
```

这证明了 AISummoner 的远程控制底座可继续演进，但不代表当前两个客户端已经达到可长期使用的产品形态。当前 Browser Controller 是用于验证功能闭环的页面集合；Remote Client 是带 AppImage 包装的命令行守护程序。Alpha 的主要目标是保留已经验证的安全与传输层，重构两端交互、Agent 运行时兼容层和桌面交付形态。

本文件覆盖 MVP-0 为赶进度设置的 UI、Provider 数量和通用化限制，但不覆盖或降低 TLS、Device Identity、owner 校验、SSH 验证、审批、超时、字节上限和 joined shutdown 等安全不变量。

## 2. 当前现状

### 2.1 已经可复用的底座

- 单管理员登录、一次性设备配对、Device owner 和 unpair 生命周期已经闭环。
- Remote 只主动出站；公网入口、WSS、yamux、SSH、PTY、exec、断线重连和 newest-wins 已有真实链路证据。
- Browser Terminal 已经能执行交互命令、处理 resize，并在断线/关闭时释放 Remote 进程。
- Agent Service 已经统一持有 Session、Turn、审批、持久事件、Remote 工具执行、超时、输出上限和取消责任。
- Fake、OpenCode 和直接 DeepSeek 已经共享相同的 AISummoner owner/审批/SSH 边界。
- DeepSeek 实测证明真实 Agent 可以连续调用 Remote 工具并完成最终回答；原累计工具次数墙已经移除。
- 生产与测试部署、Linux x86_64 命令行 AppImage、三机 E2E 和安全回归已有历史证据。

### 2.2 Controller 当前问题

- 信息架构仍是 `Devices → Device → Terminal/Agent` 的分散页面，进入 Terminal 或 Agent 会切换整页，不是持续工作的控制工作区。
- Agent 页面承担 Provider 设置、Session 恢复、消息、审批、工具展示和错误状态，职责过重。
- 只有“恢复最近会话”的最小行为，没有可用的 Session 列表、搜索、重命名、归档、明确的运行状态和会话切换体验。
- Provider 设置混在 Agent 页面里；默认 Provider、模型、健康状态和凭据状态没有统一设置中心。
- reasoning、Assistant 文本和工具虽然已有初步分离，但排版、Markdown、代码、diff、命令、错误恢复和长任务反馈仍是 MVP 级实现。
- 缺少 Agent 原生交互：停止/打断、排队消息、重试、重新生成、继续、问题询问、计划/待办、上下文与压缩状态。
- Terminal 与 Agent 无法同时作为同一控制任务的协作面板使用，也没有可保存的面板布局、折叠、最大化和全屏体验。
- 页面没有明确的 Capability 驱动；未来接入不同 Agent 时，容易把 Provider 私有行为散落到通用 UI。

### 2.3 Remote Client 当前问题

- 当前 AppImage 本质仍是 CLI：配对码写 stdout，状态和错误主要依赖 stderr/journal。
- 普通用户看不到本机是否已连接、由谁控制、当前有哪些会话，也没有本机可理解的操作记录。
- 没有“暂停/手动断开并停止自动重连”的产品操作。
- 配对码过期后缺少明确的 GUI 刷新/轮换动作。
- 没有本机权限策略、临时拒绝、能力开关或未来桌面控制授权入口。
- GUI 生命周期、后台守护、身份文件和 tunnel 生命周期尚未分离。

### 2.4 Agent 兼容层当前问题

当前 `agent.Adapter.Run` 足够证明单个 Turn，但不足以自然表达各种成熟 Agent Runtime 的长期会话能力。后续兼容层必须区分四件事：

1. **模型 API Adapter**：例如直接 DeepSeek Chat-Completions。
2. **Agent Runtime Adapter**：例如 DSH、OpenCode、Codex、Claude Code。
3. **Remote Capability Bridge**：把 shell、文件、补丁、Terminal 等操作安全地映射到选定 Device。
4. **Presentation Adapter**：把标准事件映射成 Provider/Tool 特有的文案与组件。

不能把以上职责重新塞进一个 Provider switch，也不能让 Browser 或 sidecar 变成第二个授权、Session 或执行权威。

## 3. Alpha 用户流程

Alpha 的一级流程固定为：

```text
登录
  → 选择已有设备 / 输入配对码绑定新设备
  → 进入该设备的 Control Workspace
```

- 登录成功后进入 Device Hub，而不是直接落到某个 Agent 页面。
- Device Hub 同时承担设备选择、配对、在线状态和最近活动摘要。
- 选择设备后进入长期存在的 Control Workspace；Agent、Terminal 和未来桌面画面都是该工作区内的组件，不再是彼此割裂的产品入口。
- 切换设备必须同时切换 Session、权限和面板上下文；任何旧 SSE、WebSocket 或异步请求都必须取消，不能串到新设备。

## 4. Controller 目标布局

桌面端采用受 VS Code/Zed 启发、以 Agent 为核心的三栏工作区：

```text
┌───────────────────────────────────────────────────────────────────────┐
│ Device / Runtime / Model / Connection / Turn status                 │
├───────────────┬─────────────────────────────────┬─────────────────────┤
│ Session Rail  │ Agent Conversation              │ Optional Dock       │
│               │                                 │                     │
│ + New         │ ordered messages                │ Terminal            │
│ search/filter │ reasoning (collapsible)         │ Device info         │
│ running       │ tool/approval cards             │ Activity            │
│ recent        │ final assistant output          │ Desktop (future)    │
│ archived      │                                 │ Files (future)      │
│               │ sticky composer / turn actions  │                     │
├───────────────┴─────────────────────────────────┴─────────────────────┤
│ transient status / reconnect / errors / keyboard help               │
└───────────────────────────────────────────────────────────────────────┘
```

### 4.1 左侧：Device 与 Session Rail

- 顶部显示当前 Device，可快速回到 Device Hub 或切换 Device。
- 新建 Session、搜索、最近、运行中、失败和归档分组。
- 每项显示标题、Runtime/Model、最后活动、当前 Turn 状态和未处理审批。
- 支持重命名、归档；删除要独立确认，不能与 unpair 混为一谈。
- 窄屏时折叠为图标栏或抽屉，不把三栏强行压缩到不可用宽度。

### 4.2 中间：Agent 主交互区

- 中间是唯一核心区域，长期保持 Session 上下文。
- reasoning 与最终回答必须是不同节点；reasoning 默认可折叠，流式状态可见，但不能与 final 混在同一个气泡。
- 工具调用按时间顺序留在对话中，紧凑摘要和详情分层；命令、文件 diff、搜索、错误和未知工具使用不同 renderer。
- 待审批交互接管 composer，但仍保留明确的拒绝、单次允许、当前 Session 允许边界。
- Composer 支持发送、停止/打断、排队状态、重试/重新生成；不再在每次进入 Agent 时要求选择权限模式。
- Provider 错误是可恢复的会话节点：显示安全分类、重试或切换新 Session 的动作，不把内部响应体直接吐给用户。
- Markdown、代码块、表格、复制、长输出折叠和 diff 是 Alpha 必需项，而非后续装饰。

### 4.3 右侧：Optional Dock

- 第一批组件：Terminal、Device 状态、当前控制活动。
- 后续组件：文件/变更、桌面画面、端口或任务监控。
- 面板可以开关、拖动分隔条调整大小、最大化到工作区、恢复默认布局；Terminal 在 Agent 执行期间可保持连接。
- Alpha 第一阶段采用固定三分区加右侧 tab/dock，不一开始实现任意节点拖拽的完整 IDE docking 引擎。
- 布局尺寸、折叠和最近 tab 可存 Browser 本地，但不得包含 credential、消息正文或命令输出；跨设备布局同步后置。

### 4.4 响应式与可访问性

- 大屏默认三栏；中屏左侧折叠；小屏只显示一个主区域，用底部/抽屉切换 Session、Agent、Terminal。
- 所有 resize、collapse、maximize 和 tab 切换必须可通过键盘完成。
- 保留语义标题、live region、焦点恢复和 reduced-motion；不能依赖 canvas 文本作为唯一操作入口。

## 5. DSH 参考边界

[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 是第一参考实现，不是要被直接嵌入 AISummoner 的第二套产品后端。

### 5.1 借鉴的部分

- 持久有序 Session Event Log 作为对话投影基础。
- `Session → Turn → Step → Message/Reasoning/Tool` 的交互节奏。
- 左侧 Session 列表、中间 Conversation、右侧 Details 的可调整布局。
- reasoning 折叠、工具节点 registry、审批接管 composer、队列/状态反馈。
- Provider/Service/Consumer 的能力边界，以及 UI slot/renderer 的扩展思路。
- 模型设置、能力提示和 Provider 状态的产品组织方式。

### 5.2 明确不引入的部分

- 不引入 Cordis/DSH 插件 Host 作为 AISummoner 的主运行时。
- 不复制 DSH 的 Session 数据库或凭据目录。
- 不允许 DSH 默认本地 shell、文件系统或 Terminal 访问 Server 宿主机。
- 不让 DSH UI 直连 Provider、Remote 或成为第二个 owner/审批权威。
- 不把整个 DSH Web 源码 fork 进来长期维护。可以参考 MIT 实现，必要的小段复用必须保留归属和第三方声明；默认优先按交互合同独立实现。

DSH 当前仍是 pre-1.0 developer preview，因此集成必须锁定精确版本和协议 fixture，不能依赖未版本化的内部组件路径。

## 6. 统一 Agent Runtime 架构

### 6.1 唯一事实来源

AISummoner 的 Session Log 继续是产品唯一事实来源：

- Server 决定 user、Device、Session、Turn、approval 和 tool execution 的绑定。
- Runtime 原生 Session/Thread ID 可以作为受限 metadata 或恢复句柄持久化，但不能替代 owner predicate。
- Browser 只读取标准事件；不持久化或解释 Provider 私有 wire payload。
- Runtime 崩溃、升级或切换后，历史 UI 仍可由 AISummoner 事件重放。

标准事件族至少要覆盖：

- Session created/renamed/archived/status/capabilities；
- Turn queued/started/steered/canceled/completed/failed；
- user/assistant/reasoning delta 与 completed；
- tool proposed/pending/approved/denied/started/output/completed；
- question/request-user-input、plan/todo/status；
- usage/context/compaction 和安全分类后的 Provider error。

事件必须有稳定 ID、Session/Turn 关联、单调顺序和 replay 语义。大内容要单独有界，不能让 SSE snapshot 无限增长。

### 6.2 Runtime Adapter

下一代接口应从“一次 Run 函数”演进为能力声明加有生命周期的 Runtime Session：

```text
Describe / Health / ListModels
CreateSession / ResumeSession / CloseSession
StartTurn / SteerTurn / CancelTurn
Stream normalized events
Resolve approval / question
Compact or report context when supported
```

每个 Adapter 返回 Capability Descriptor，例如：

- resume、fork、steer、interrupt；
- reasoning、usage、context window、compaction；
- tool approval、question、plan/todo；
- supported Remote capabilities；
- authentication kind、health and model discovery。

UI 必须根据 Capability Descriptor 渐进增强：不支持的动作隐藏或禁用并说明原因，不能靠 Provider 名称硬编码猜测。

### 6.3 Provider 配置

Controller Settings 提供 Provider Profile：

- runtime 类型、显示名、启用状态、默认模型、可用模型、健康与版本；
- credential 状态只显示“已配置/需要配置/失效”，永远不回显 secret；
- 默认 Provider 作用于新 Session，现有 Session 和 in-flight Turn 不被暗中切换；
- 可在新 Session 时覆盖 Provider/Model，Session 头部始终显示实际绑定；
- credential 最终应由 Server 侧加密存储或外部 secret reference 管理。当前 DeepSeek 进程内存输入保留为过渡方案，不能扩展成 Browser localStorage。

### 6.4 Remote Capability Plane

成熟 coding agent 不能只靠一个通用 `remote_exec` 达到原生体验。能力面按安全优先逐步扩展：

1. 结构化 shell exec/PTY；
2. 文件读取、目录/搜索；
3. 有界写入、patch、diff 和变更审阅；
4. 任务、进程和端口状态；
5. 未来桌面画面与输入。

所有能力必须绑定当前 owned Device 和 Agent Session；Remote 的本机权限策略可以进一步拒绝，但不能反向授予 Server 未授权的能力。DSH/OpenCode/Codex/Claude 的本地工具若不能安全重定向到该能力面，就必须禁用。任何 sidecar 都不得把 Server workspace 当成被控设备 workspace。

## 7. Runtime 适配顺序

### 7.1 DSH：首个丰富适配与 UI 参考

- 锁定一个精确 DSH 版本，先做协议/SDK spike，再决定长期 sidecar 方式。
- 用 DSH 的 Session/Event/Tool 思路验证 AISummoner 标准事件 v2 和对话 UI。
- 禁用 DSH 默认本地 shell/filesystem，提供指向 AISummoner Remote Capability Bridge 的替代 Provider。
- 不使用“允许所有本地工具”的 SDK 默认示例。
- 验收重点是长会话、流式 reasoning/final、工具、审批、取消、恢复和错误，而不是把 DSH 页面 iframe 进来。

### 7.2 OpenCode：升级现有 Adapter

- 保留现有 loopback isolation、Basic Auth、固定版本和双层本地工具 deny。
- 补齐原生 Session 列表/恢复、取消、usage、权限请求和完整事件映射。
- 在锁定版本上比较 HTTP/SSE 与 ACP 的支持范围后再选择；当前 [OpenCode Server](https://dev.opencode.ai/docs/server/) 和 [CLI](https://dev.opencode.ai/docs/cli/) 都可作为契约来源，但不能无迁移验证地切换传输。

### 7.3 Codex：官方 App Server 优先

- 优先评估官方 [Codex App Server](https://developers.openai.com/codex/app-server)，映射 Thread/Turn/Item、审批与 delta。
- 以 Server 本机 stdio 或 Unix socket 连接，锁定 Codex 版本并使用生成 schema；实验性 WebSocket 不作为生产边界。
- Remote 能力通过受限 MCP/custom tool bridge 映射，禁用或隔离会访问 Server 本地 workspace 的默认能力。
- Codex SDK 可用于一次性自动化或测试，但丰富 Controller 集成优先使用 App Server 协议。

### 7.4 Claude Code：官方 Agent SDK

- 使用官方 Agent SDK 的持续 streaming input 模式，不解析 Claude Code TUI/ANSI 输出。
- 映射部分消息、tool use/result、permission callback、interrupt/resume 和 usage。
- 通过 custom MCP/permission callback 接入 AISummoner Remote Capability Plane。
- 参考 [Claude Agent SDK streaming input](https://code.claude.com/docs/en/agent-sdk/streaming-vs-single-mode)；版本、授权方式和商业条款在实现任务中单独冻结。

直接 DeepSeek 继续作为轻量模型 API Adapter 和可用 fallback reference，但丰富 Agent Runtime 的实现优先级固定为：

```text
DSH → OpenCode → Codex → Claude Code
```

## 8. Remote Client GUI

### 8.1 进程模型

目标采用“Core Daemon + Desktop UI”双进程边界：

```text
Desktop UI / AppImage
  ↕ private Unix domain socket (0600, peer UID checked)
Remote Core Daemon
  ├─ Device Identity (never returned to UI)
  ├─ Tunnel / reconnect / Embedded SSHD
  ├─ bounded sanitized event ring
  └─ local restrictive permission policy (future)
```

- daemon 保持现有 Go Tunnel/SSH 核心，可由 systemd 独立运行，也可在桌面用户会话中受控启动。
- GUI 关闭不应默认断开 daemon；用户明确选择 Disconnect/Pause 才停止连接。
- UI 不读取私钥，不开公网/局域网 TCP listener，不通过日志接收配对码。
- GUI 技术先做小型打包 spike。首选评估 Go + Web UI shell（例如 Wails），但必须先证明 Ubuntu/AppImage 对 WebView 依赖和非 root 安装可控；失败时保留 Fyne 或私有 loopback UI 备选。核心 IPC 不与 toolkit 绑定。

### 8.2 Alpha GUI 必需能力

1. **设备与配对**
   - 显示 Device ID、Server、identity 状态；
   - 显示当前配对码和明确的过期倒计时；
   - 一键复制、显式刷新/轮换配对码；旧码立即失效；
   - 已配对时显示绑定状态，不长期展示历史配对码。
2. **连接与被控状态**
   - Connecting/Online/Offline/Paused/Error；
   - 最近心跳、重连倒计时、当前 Server TLS 状态；
   - 当前活跃 Terminal/Agent/未来 Desktop 会话的数量与开始时间。
3. **本机活动记录**
   - 只保存结构化安全摘要：连接、配对、Terminal/Agent 会话开关、执行成功/失败、策略拒绝；
   - 默认不保存命令、参数、Terminal 输入输出、Agent 对话、credential 或私钥；
   - 内存 ring 有硬上限，持久日志可配置容量和保留期。
4. **手动断开**
   - `Disconnect/Pause` 关闭当前 Tunnel 和全部子会话，并暂停自动重连；
   - `Resume` 才重新连接；
   - 默认保留 identity 与配对关系；`Unpair/Reset identity` 是独立危险操作并二次确认。
5. **未来权限管理**
   - 本机策略只能收紧 Server 已授权能力：Terminal、Agent exec、文件读/写、桌面查看/输入；
   - 支持 always deny、ask locally、allow while unlocked 等模式；
   - 本机 deny 必须快速传播到 Controller，并产生不含敏感载荷的审计事件。

### 8.3 必要协议补充

- 增加显式 pairing refresh/rotate，而不是让用户靠重启进程获得新码。
- 为控制流补充安全的 session purpose 和 lifecycle metadata，使 Remote GUI 能区分 Terminal、Agent 与未来 Desktop；不能包含原始命令/消息。
- 增加私有本地 IPC API：status、pairing、pause/resume、recent events、policy；所有请求有界并校验同 UID。

## 9. 分阶段实施

| 阶段 | 目标 | 主要交付 | 退出条件 |
|---|---|---|---|
| A0 | 方向与仓库收口 | 本基线、ADR、路线图、命令源码追踪修复 | 文档一致、无业务改动 |
| A1 | Remote Core + IPC | daemon 状态机、脱敏事件、同 UID Unix socket、pause/resume/配对刷新 | headless 兼容、joined 生命周期、IPC 安全测试 |
| A2 | Qt Remote GUI | Qt 6 Widgets 状态/事件/设置、GUI AppImage | Ubuntu 非 root 安装与真实 Remote E2E |
| A3 | Controller Workspace Foundation | Device Hub、三栏 shell、可调整布局、Session rail、Terminal dock、Settings 入口 | 旧功能无回退，设备切换无跨设备事件 |
| A4 | Agent Domain/UI v2 + DSH | 标准事件 v2、Capability Descriptor、DSH adapter spike/实现、DSH 风格 Conversation | 长会话/恢复/审批/取消/错误 E2E |
| A5 | OpenCode Rich Adapter | 完整 Session/Turn/cancel/usage/permission 映射 | 与 DSH 共用同一 UI，无 Provider 分叉页面 |
| A6 | Codex Adapter | App Server Thread/Turn/Item、审批、Remote tools | 锁版契约测试和真实远程 coding Turn |
| A7 | Claude Code Adapter | Agent SDK streaming、permission、Remote tools | 锁版契约测试和真实远程 coding Turn |
| A8 | 扩展控制能力 | Files/diff、桌面显示、Remote 权限策略 | 每项独立 threat model 与人机验收 |

阶段顺序表达产品优先级，不授权一次性跨阶段大改。每个阶段继续按窄任务计划、实现 summary、独立 review 和真实 E2E 交付。

## 10. 质量与验收原则

- 先写可交互 wireframe、事件/API 合同和 mutation-sensitive 测试，再迁移页面。
- Controller 关键 E2E：登录→Device Hub→Workspace→切 Session→Agent+Terminal 同屏→断线/重连→unpair。
- 每个 Runtime Adapter 必须有固定版本 fixture、错误分类、取消/join、secret redaction 和 capability matrix。
- 每个 Runtime 的同一 UI 验收必须覆盖 reasoning/final 分离、工具生命周期、审批、失败恢复、Session resume。
- Remote GUI 必须在 Ubuntu 非 root 用户下验证首次配对、过期刷新、断开不重连、恢复、日志脱敏和 AppImage。
- 不以“能看到页面”代替数据面验收，不以 Provider 免费额度或外部服务健康作为确定性 CI 前提。
- 大改必须保留回滚路径；数据库 migration 只向前且先设计兼容读取期。

## 11. 明确不做的捷径

- 不直接维护/iframe DSH Web 作为最终 Agent 页面。
- 不为每个 Agent Runtime 复制一套 Web 页面。
- 不解析 Codex/Claude TUI 文本或 ANSI 作为正式协议。
- 不给任何 sidecar Server 本地 shell 或真实工作区来模拟 Remote。
- 不让 Browser 保存 Provider key、Remote identity 或控制日志敏感正文。
- 不把 GUI 和 daemon 合成一个“窗口关掉就意外断线”的不可维护进程。
- 不在 Alpha 工作区重构时顺带加入多用户、P2P、文件管理和桌面输入。

## 12. 实施前仍需逐项冻结的决策

- Controller split/dock 采用自研最小布局还是成熟布局库；先以键盘可访问性、bundle 和维护成本做 spike。
- DSH 的固定版本、支持 API/SDK 面和 sidecar 生命周期。
- Provider credential 的长期加密存储或外部 secret reference 方案。
- Runtime 原生 Session ID 的恢复/迁移策略和标准事件 v2 schema。
- Remote GUI toolkit 与 AppImage/WebView 依赖方案。
- Remote 文件能力的最小原语和写操作审批粒度。
- Desktop 仅查看还是可输入；必须另立 ADR 与 threat model，不能由本文件默认授权。
