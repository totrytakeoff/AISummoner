# AISummoner 公开 Roadmap

本文只描述方向与完成口径，不承诺日期。详细任务状态保存在
[`docs/agent_context/roadmap.md`](agent_context/roadmap.md)。

## 已完成：MVP-0 纵向闭环

- Browser 登录、一次性配对和 Device owner；
- Remote 主动 WSS/yamux Tunnel 与严格 SSH Host Key 验证；
- Terminal PTY、交互、resize 与关闭回收；
- Agent Session、审批、Remote exec、超时和输出限制；
- Fake、直接 DeepSeek 与 OpenCode 的真实链路验证；
- 单节点 Server 部署和 Linux CLI AppImage。

## 已完成：Alpha 第一阶段

- Remote Core daemon 与同 UID 私有 Unix IPC；
- Qt 6 Widgets Linux GUI 和零配置 GUI+daemon AppImage；
- Device Hub → 三栏 Control Workspace；
- DSH-first Controller 视觉与交互基线；
- 真实 DSH Runtime → Capability Bridge → Remote SSH 链路；
- Session 权限、凭据恢复、有序 replay、折叠、归档和删除。
- DSH 原生多供应商配置与当前 Session 的模型/推理强度切换。

这构成当前“控制端初具雏形”的阶段性成果，但仍需要持续体验打磨和发布工程。

## 下一阶段：Agent 体验与 Runtime 兼容层

1. 补齐标准事件 v2、Capability Descriptor 和长期 Runtime Session 生命周期。
2. 完善 DSH 的 cancel/steer/retry/queue、Markdown、代码、diff、计划与上下文体验。
3. **OpenCode**：原生 Session/Turn/cancel/usage/permission 映射。
4. **Codex**：基于官方 App Server 机器接口和锁定 schema。
5. **Claude Code**：基于官方 Agent SDK 的 streaming/permission/resume。
6. 保留 Direct DeepSeek 轻量适配与 Fake 确定性测试层。

所有 Runtime 必须共用 AISummoner 的用户、Device、Session、审批和 Remote
Capability 权威；不能引入 Browser 直连 Provider 或 Server 本地 shell 回退。

## 跨平台 Remote Client

- **Linux**：继续稳定 Qt AppImage、安装/更新、权限提示和真实 Ubuntu 验收。
- **Windows**：复用 Go Remote Core 状态机和 Qt UI；首版采用普通用户会话内的后台 Core，
  新增带 logon-SID 验证的 Named Pipe、DPAPI Device Identity、PowerShell/ConPTY、Job Object
  进程树回收和原生 Qt 打包。兼容性/安全 spike、共同 Core 平台接口和真实 Windows
  Core/DPAPI Identity/认证 IPC、PowerShell/Job exec 和 ConPTY Terminal 已完成并通过真实
  原生链路 CI；下一步依次完成普通用户 GUI/干净机验收与 Agent profile，最后才签名发布。
  Git Bash 可在未来
  作为显式可选 profile，但不会内置或成为前置依赖；Core 也不会直接注册为 LocalSystem
  Service。详细方案见
  [Windows 被控客户端移植方案](design/windows-remote-client-port.md)。
- **macOS**：在 Windows 边界稳定后再评估，不提前承诺。

Windows 不是简单交叉编译：Unix socket、`SO_PEERCRED`、PTY、信号、文件权限与 daemon
生命周期都需要平台化设计和独立 threat model。

## 更后续能力

- Device-bound 文件读取、搜索、写入、补丁与 diff；
- Remote 本机细粒度权限和可审计策略；
- 桌面画面与输入（必须先有独立 threat model/ADR）；
- Session rename/fork、长会话压缩和更丰富 Workspace 组件；
- 多用户/RBAC、集群、端口转发等独立项目。

## 始终不变的门槛

- 不削弱 TLS、Device challenge、owner、严格 SSH、审批、超时和输出上限；
- 新 Runtime 先锁版本、fixture、错误/cancel/secret 测试，再做真实 Remote proof；
- 新平台必须证明后台生命周期、私有 IPC、身份文件权限和子进程回收；
- 发布物必须有可复现构建、SHA-256、最低系统验证和明确回滚路径。
