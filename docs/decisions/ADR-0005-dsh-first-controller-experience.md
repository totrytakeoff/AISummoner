# ADR-0005：DSH-first Controller 体验基线

- 状态：Accepted
- 日期：2026-08-23
- 决策范围：Alpha Controller 的信息架构、交互与视觉实现
- 关联基线：`docs/baseline/04-alpha-product-direction.md`

## 背景

Task017 已证明 Device-scoped Workspace、Session rail、Agent center 与可选
Terminal dock 的技术结构可行，但其页面层级、视觉语言和 Agent 交互仍沿用了
MVP 验证界面。继续在该风格上局部修补会让 Controller 同时维护一套自创交互
和一套未来的 DSH adapter 交互，无法形成一致产品。

用户已明确指定 `/home/myself/workspace/deepseek-harness` 为 Controller 的第一
产品基线：DSH 是一等公民，先完整承接其成熟的 Session、Conversation、Tool、
Approval、Composer 和 Settings 操作逻辑，再通过适配层逐步接入 OpenCode、Codex
与 Claude Code。

## 决策

### 1. DSH Web UX 是 Controller 的规范实现参考

锁定本地 DSH checkout `47f943859bef60e4160492346772ded9b24f765a`。Controller
直接对齐其布局尺度、设计 token、Session 行、Conversation 节点、reasoning
披露、tool disclosure、审批区、浮动 composer 和 Settings modal。允许移植其
MIT 授权的实现片段；凡构成实质复用的文件必须保留第三方声明和版本归属。

这不是“DSH-inspired”或自由发挥的配色参考。除 AISummoner 必需的 Device、
Terminal 和权限语义外，默认交互应以 DSH 行为为准；偏离必须在任务计划中说明
原因并有测试。

### 2. DSH-first 不改变 AISummoner 的安全权威

AISummoner Server 继续唯一决定 User、Device owner、Session、Turn、approval
和 Remote capability。DSH 的本地 credential/session store、默认 shell/filesystem
工具和后端 Host 不进入该边界。Browser 只消费 AISummoner 标准事件，不直连
Provider 或 Remote。

因此 Controller 分为两层：

- **Experience/Presentation Adapter**：以 DSH 为默认 profile，负责布局、文案、
  capability 可见性和标准事件 renderer；
- **Runtime Adapter**：DSH、OpenCode、Codex、Claude Code 等实际运行时，负责
  Session/Turn/stream/cancel/tool 等机器协议。

当前 Runtime 仍为 DeepSeek/OpenCode/Fake 时，UI 必须如实显示实际 Runtime，
不能把 DSH 体验 profile 冒充为已接入的 DSH runtime。

### 3. 产品一级路径收敛

一级路径固定为 `Login → Device Hub → Device Workspace`。旧的 Device Manage 页面
退役；Device metadata、unpair、Provider/Model 与一般设置进入 Workspace 内的
DSH-style Settings modal。旧 URL 只做安全迁移跳转，不再渲染旧页面。

### 4. 渐进交付完整 DSH 能力

本次先完成可见的 DSH shell、conversation、tool/reasoning、composer 与 Settings
基线，并为 capability/runtime adapter 留出显式接口。Queue、steer、cancel、retry、
rename/archive/fork、Markdown/diff renderer 等只有在 Server 协议真实支持后才启用；
不绘制会误导用户的空按钮。

后续 Runtime 顺序保持：

```text
DSH → OpenCode → Codex → Claude Code
```

## 后果

- Controller 不再维护旧 dashboard/manage 视觉与第二套 Agent 交互。
- DSH 上游变化需通过锁定 commit、第三方声明和交互 fixture 有意识地吸收。
- AISummoner 特有的 Device/Terminal/approval 功能必须融入 DSH shell，而不是另开
  不一致页面。
- “体验 profile 已完成”与“某 Runtime adapter 已完成”在文档和 UI 中分别陈述。

## 被拒绝的备选方案

### 继续微调 Task017 的自创视觉

它保留了用户已否定的 Manage 页面、双层 chrome、重卡片与非 DSH composer，
会让后续 adapter 工作反复重写 UI。

### 直接 iframe 或信任 DSH 后端

这会引入第二个 Session、credential、tool 和本地执行权威，破坏当前 owner 与
Remote capability 边界。允许移植 Web UX，不允许移交安全权威。
