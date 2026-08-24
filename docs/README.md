# AISummoner 文档导航

本文档目录同时服务于使用者、贡献者和后续编码 Agent。首次接触项目建议从根目录
[README](../README.md) 和 [快速开始](quick-start.md) 阅读。

## 使用与开发

- [快速开始](quick-start.md)：本地 Fake Agent、Linux Remote Client 和基本流程。
- [部署指南](deployment.md)：Compose/Caddy、DSH Runtime、headless systemd 与安全边界。
- [公开 Roadmap](roadmap.md)：当前阶段、Runtime 顺序和跨平台计划。
- [参与开发](../CONTRIBUTING.md)：环境、测试、提交与文档约束。
- [安全策略](../SECURITY.md)：支持范围、报告方式和核心不变量。
- [MIT License](../LICENSE) 与
  [第三方声明](../THIRD_PARTY_NOTICES.md)：项目和移植代码的许可边界。

## 产品与架构基线

这些文件是实现行为的权威来源：

1. [产品与范围](baseline/00-product-scope.md)
2. [架构与技术栈](baseline/01-architecture-stack.md)
3. [协议、数据与安全](baseline/02-protocol-data-security.md)
4. [MVP 实施与验收计划](baseline/03-mvp-plan.md)
5. [Alpha 产品与双客户端方向](baseline/04-alpha-product-direction.md)

## 架构决策记录

- [ADR-0001：MVP 技术栈](decisions/ADR-0001-mvp-stack.md)
- [ADR-0002：OpenCode Runtime](decisions/ADR-0002-opencode-runtime.md)
- [ADR-0003：统一 Agent/Web 适配层](decisions/ADR-0003-agent-adapter-ui.md)
- [ADR-0004：Alpha 双客户端与 Runtime 架构](decisions/ADR-0004-alpha-clients-and-agent-runtime.md)
- [ADR-0005：DSH-first Controller 体验](decisions/ADR-0005-dsh-first-controller-experience.md)
- [ADR-0006：Runtime 供应商配置与会话模型边界](decisions/ADR-0006-runtime-provider-model-configuration.md)

## 设计文档

- [Remote Client 私有 IPC v1](design/remote-client-ipc-v1.md)
- [Qt Remote Client](design/remote-client-qt.md)

## 验收与运行证据

`acceptance/` 保存真实链路和部署证据，不是安装向导：

- [MVP-0 三机验收](acceptance/mvp-0-2026-08-13.md)
- [Task011 测试部署](acceptance/task011-test-deployment-2026-08-21.md)
- [Task014 Web Key 入口](acceptance/task014-web-key-entry-2026-08-23.md)

带 `cleanup`、`unblock` 的文件保留当时的故障、回滚和环境事实，用于审计历史。

## 编码 Agent 协作记录

`agent_context/` 是 Codgent 工作流的耐久交接区：

- `project_context.md`：当前项目事实；
- `architecture_analysis.md`：架构形态与风险；
- `roadmap.md` / `todo.md` / `state.json`：内部执行状态；
- `tasks/taskNNN/`：每项任务的 plan、summary 和 review。

这些文件保留详细失败/重试证据，内容会比公开 Roadmap 更细。外部贡献者通常不需要
逐项阅读，但修改信任边界、协议或架构时必须同步对应 baseline/ADR。

## 权威顺序

```text
已接受 ADR
  > 当前 baseline
  > 设计文档与任务计划
  > README / 快速指南
  > 历史愿景与验收快照
```

验收记录描述的是特定时间和环境；它不会自动保证当前公共服务或二进制仍在线。
