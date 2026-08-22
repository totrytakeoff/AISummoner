# ADR-0003：统一 Agent 调用与 Web 交互适配层

- 状态：Accepted
- 日期：2026-08-21
- 决策范围：MVP-0 后的 Agent 扩展与 WebUI
- 覆盖：MVP-0 对第二个 Agent Provider 的临时范围限制

## 背景

MVP-0 为验证远程执行链路，只实现了 OpenCode Provider 和确定性 Fake Adapter。现有 WebUI 又把消息与工具调用分别保存和渲染，导致工具统一堆在对话末尾；Fake 模式没有显著标识，用户会把固定测试输出误认为真实 Agent 回答。这两点都不适合作为后续接入多个 Agent Runtime 的基础。

DeepSeek Harness 的会话投影提供了有价值的参考：持久事件是事实来源，消息、工具和状态按事件顺序组装成会话节点；会话层决定节点位置，工具层决定展示方式；待审批交互接管输入区域。AISummoner 借鉴这些职责划分，但不引入 Cordis、DSH 插件运行时或其完整前端实现。

## 决策

1. Go Server 保留统一 `agent.Adapter` 调用接口。每种 Agent Runtime 的 Adapter 负责把原生会话、流式文本、工具调用、取消和错误归一化为 AISummoner 领域事件。
2. Browser 只消费 AISummoner 领域事件，不直接理解 OpenCode、Codex、Claude 等 Provider 的私有 wire schema。
3. Web 会话使用单一有序 timeline。用户消息、折叠的 reasoning、Assistant 最终文本和工具调用按首次领域事件的位置进入 timeline；后续 delta 或状态只更新原节点，不改变顺序。Reasoning 与最终回答是不同领域事件和持久化消息，不允许混在同一个输出框。
4. Web 提供独立的 Provider presentation adapter 和 Tool presentation adapter。Provider adapter 只定义名称、能力提示与状态文案；Tool adapter 按规范化工具名选择摘要和详情展示；未知 Provider/Tool 必须使用安全的通用 fallback。
5. 待审批工具调用接管会话输入区，审批完成后恢复输入。工具在 timeline 中保留为紧凑、可展开的执行记录，避免重复显示同一组审批控件。
6. Fake Adapter 必须显式标为测试运行时，并明确说明它不理解自然语言。不得把 Fake 的固定输出呈现成真实模型回答。
7. OpenCode 继续作为第一种真实 Provider。新增其他 Provider 时必须同时实现调用适配、错误映射、能力声明、事件契约测试和至少一条 UI 投影测试；不得绕过现有 owner、审批、SSH、超时和输出上限。
8. 本 ADR 不授权 Browser 直接连接 Provider，也不授权 Provider 获得 Server 本地 shell。所有远程执行仍必须经过规范化工具、AISummoner 审批和绑定到目标 Device 的 SSH 通道。
9. Server 为每个 Device 提供 owner-checked 的最近 Session 快照。页面重入默认恢复该会话和审批范围；审批模式只在用户显式创建新对话时选择，`full_access` 仍只属于该 Session。
10. OpenCode 的 idle 只是状态信号，不能单独证明 Turn 成功。Adapter 必须等到明确的非工具 final assistant finish；空 Assistant 占位后的 idle 仍需继续读取随后可能到达的 Provider error。
11. DeepSeek 作为第二个真实 Provider 直接实现同一 `agent.Adapter`。它使用
    Server-owned 有界 user/assistant 历史、分离的 reasoning/final 事件与唯一
    `remote_exec` 工具；不引入 DSH 后端、第二个会话事实来源或 Server
    本地执行权。DeepSeek/OpenCode/Fake 都必须继续经过同一 owner、审批、
    Tunnel/SSH、超时和输出上限。Thinking 工具循环按 Provider 合同回传当轮
    `reasoning_content` 和非 null assistant content，并且不得发送当前
    DeepSeek Thinking API 明确拒绝的 `tool_choice` 字段。
12. Agent 页面无会话时自动创建默认 `per_command` 会话，`New conversation`
    也不再弹出模式选择器。用户只能在待审批命令上通过额外确认把当前
    会话提升为 `full_access`；新会话仍回到默认逐命令审批。
13. 单管理员可在登录后的 Agent 页面短暂输入 DeepSeek Key，Browser 使用
    固定默认模型；非 Browser API 客户端仍可显式选择模型。Browser 仍不直连
    Provider，也不持久化 credential；Server 通过精确 Origin、认证和有界 JSON
    接收后，只在进程内存中注册固定官方 HTTPS origin 的 Adapter。
    新建 Session 绑定当前 Provider，旧 Session 保持其持久化 Provider，运行中
    Turn 不被替换。Server 重启后需重新输入；无人值守部署仍使用 secret 注入。
14. 正常 Agent Turn 不设累计 Tool Call 次数墙。工具循环由 Turn 总时限、
    Provider idle/request 上限、单命令超时、参数与输出字节上限、审批和取消共同
    收敛；单个不可信 Provider 响应中的批量 Tool Call 数仍有独立协议输入上限。
    该协议上限不得复用成跨多轮 Provider step 的产品工作量限制。

## 后果

正面：

- 对话顺序符合真实 Agent 运行过程；
- Provider 与 UI 不再互相绑定；
- 新工具可以拥有专属展示，同时未知工具仍可读；
- Fake 与真实模型不会再被混淆；
- 后续增加 Provider 不需要复制整套 Agent 页面。

负面：

- Provider 能力需要成为显式产品数据，而不能靠页面猜测；
- 取消、队列和更丰富的推理摘要仍需继续扩展领域事件；
- 不采用 DSH 完整插件框架意味着 AISummoner 仍需维护自己的轻量 registry 和测试。

## 被拒绝的备选方案

### 直接把 DSH Web 作为 AISummoner Agent 页面维护

DSH 的 Host、Session Log、插件树和本地执行环境与 AISummoner 的 Device owner、Tunnel、SSH 和远程审批边界不同。直接嵌入会产生两个会话真相来源，并增加不必要的运行时和供应链面。

### 每种 Agent 单独实现一套页面

这会复制消息流、工具状态、审批、安全提示和错误处理，并让不同 Provider 的行为持续漂移。

### 在 Browser 解析每个 Provider 的原生事件

这会把 Provider 凭据和协议兼容问题推到公网客户端，也会绕过 Server 的持久化、授权和错误脱敏责任。
