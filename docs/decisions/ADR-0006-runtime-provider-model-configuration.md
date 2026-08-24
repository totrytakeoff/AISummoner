# ADR-0006：Runtime 供应商配置与会话模型边界

- 状态：Accepted
- 日期：2026-08-24
- 关联：ADR-0003、ADR-0004、ADR-0005、Task022

## 背景

首轮 DSH 接入只允许写入官方 DeepSeek API Key，并在每次 Turn 前检查固定的
`DEEPSEEK_API_KEY`。这无法表达 DSH 已原生支持的多供应商、兼容网关、自托管模型、
模型目录和推理强度，也会诱使后续 OpenCode、Codex、Claude Code 把各自配置格式泄漏
到 Web UI。

同时必须保持一个既有不变量：`agent_sessions.provider` 表示执行会话的 Runtime
Adapter（例如 `dsh`），而不是 Runtime 内部的模型供应商（例如
`deepseek-official` 或 `acme-gateway`）。模型切换不能暗中更换产品 Session、Device、
审批策略或 Remote Capability 绑定。

## 决策

### 1. Runtime 与模型供应商是两层身份

AISummoner 继续把 Runtime 名称持久化到产品 Session。供应商、模型和可选推理强度
由 Runtime 自己管理，并以完整选择值作用于该 Runtime Session 的下一步。Web 不把
供应商 ID 写回 `agent_sessions.provider`，SQLite 也不复制 Runtime 的模型目录。

### 2. Agent 域提供两个可选能力

- `RuntimeConfigurationAdapter`：返回脱敏供应商目录，并执行带修订号的配置或删除；
- `RuntimeSessionAdapter`：创建/恢复不透明 Runtime Session，读取模型目录，并选择
  完整的供应商、模型和推理强度。

不实现这些能力的 Fake、Direct DeepSeek 或旧 OpenCode Adapter 保持原行为。HTTP 和
Web 只依赖通用投影，不读取 DSH schema、凭据引用或配置文件路径。

### 3. DSH 是当前事实来源

DSH Adapter 使用固定版本 Host 的公开 RPC：

- `llm.providers`；
- `settings.describe` / `settings.mutate`；
- `credentials.describe` / `credentials.set` / `credentials.unset`；
- `session.models` / `session.selectModel`。

Provider Settings 只返回 route/display、状态、受控 Base URL/API/model 字段、修订号，
以及 `configured/writable` 两个凭据事实。API Key 只沿 Browser → AISummoner → DSH 的
写方向传递；引用名、来源、值、raw settings/schema 和 raw Provider 错误均不返回。
配置修改使用最小 path operation，DSH 未暴露的用户字段必须保留。

### 4. 模型操作与 Turn 准入串行

产品只持久化已有的 DSH 不透明 Session ID。首次打开模型菜单可惰性创建该 Session。
同一个产品 Session 的准备、读取和选择与 Turn 准入互斥：任一方已进入时，另一方
返回稳定冲突，不允许出现半应用选择或已落库但使用旧模型的用户消息。

Turn preflight 读取当前 DSH route 的 routability 和脱敏凭据状态。缺少已命名凭据或
route 已不可用时，在持久化用户消息前失败；无需托管凭据的 Provider 仍可由真实请求
自行证明认证能力。修复配置或选择其他模型后，原产品/DSH Session 可继续使用。

### 5. 后续文件型 Adapter 使用 managed-document 事务

OpenCode、Codex 或 Claude Code 若必须修改配置文件，其 Adapter 必须：

1. 只解析 Server 固定、受管理的配置根，不接受 Browser 路径；
2. 拒绝 symlink、路径逃逸、超限文件和 malformed live document；
3. 修改 Adapter 明确拥有的键，保留全部未知字段；
4. 先验证完整候选，再用同目录私有临时文件、`fsync` 和 atomic rename 提交；
5. 多文件切换先捕获精确快照，后续提交失败时有界回滚；
6. credential-bearing 文件保持 `0600`，secret 不进入响应、日志、审计或 Browser
   storage；
7. 只有 live 配置成功后才发布“当前 profile”。

该合同借鉴 [CC Switch](https://github.com/farion1231/cc-switch) 的事务与回滚思路，
但不复制其应用路径或把 OpenCode 的
additive provider 配置误建模成 Codex/Claude 的 switch-only profile。

## HTTP 投影

```text
GET    /api/v1/agent-runtimes/{runtime}/providers
PUT    /api/v1/agent-runtimes/{runtime}/providers/{provider}
DELETE /api/v1/agent-runtimes/{runtime}/providers/{provider}
GET    /api/v1/agent-sessions/{session}/models
PATCH  /api/v1/agent-sessions/{session}/models
```

所有请求都需要管理员 Session；全部写请求（包括 `PUT`）必须通过精确 Origin 校验。
Body、ID、字符串、模型数量和 Runtime 回复均有上限。配置写入使用
`expected_revision`，冲突必须刷新后重试，不能 last-write-wins。

## 结果

- DSH 可配置官方或兼容供应商、自定义模型，并在原会话切换模型/推理强度；
- Web 与未来 Runtime 的配置格式解耦，Runtime 仍保留原生验证和生命周期语义；
- Provider Settings 是 Host 级配置，Composer 是当前 Session 选择，两者不混为一个
  “全局模型”开关；
- DSH 配置成功但凭据写失败可能形成可刷新、可重试的部分结果，不能谎报为完全未改；
- 当前不实现 OAuth、自动模型发现、Provider failover，也不修改 OpenCode/Codex/
  Claude Code 文件。

## 未采用方案

- **Browser 直接连接 DSH/Provider**：绕过 AISummoner 鉴权、同源和脱敏边界；
- **把供应商写进 `agent_sessions.provider`**：混淆 Runtime 与模型身份并破坏旧会话恢复；
- **复制 DSH settings/credential 到 SQLite**：产生第二事实来源和 secret 扩散；
- **立刻实现通用配置文件 writer**：尚无具体 Runtime 合同，容易覆盖用户已有配置；
- **只保留一个 API Key 输入框**：无法满足已验证的多 Provider/模型工作流。
