# AISummoner MVP-0 产品与范围基线

状态：**已冻结**
版本：`0.1`
日期：`2026-08-12`

## 1. MVP-0 的目的

MVP-0 只回答一个问题：

> 用户能否从任意浏览器，把服务端 Agent 和交互式终端可靠地接到一台主动联网的 Linux 设备上？

它是用于验证产品核心价值和技术主链路的 Demo，不等同于安全审计完成、可公开运营或可承诺 SLA 的版本。

## 2. 目标用户和使用环境

MVP-0 只服务一个场景：

- 一个自托管实例；
- 一个管理员账号；
- 少量属于管理员本人的 Linux 设备；
- Server 有可被 Remote Client 访问的 HTTPS/WSS 地址；
- Remote Client 只需要出站网络，不需要公网 IP 或入站端口；
- 用户接受 Agent 获得当前 Remote Client 进程用户的命令执行权限。

## 3. 必须完成的用户故事

### P0-01：启动并注册设备

用户在 Linux 机器执行：

```bash
aisummoner-client start --server https://example.com
```

客户端首次启动生成长期 Ed25519 设备身份，连接 Server 并显示一次性配对码及过期时间。

### P0-02：从 WebUI 配对

管理员登录 WebUI，输入配对码后：

- 配对码立即失效；
- 设备归属于当前管理员；
- 设备列表显示名称、平台、架构、版本和在线状态。

### P0-03：打开交互式终端

管理员从设备页打开 Terminal 后，可以：

- 输入和接收终端数据；
- 使用 Bash/Zsh 等用户 Shell；
- 运行 `vim`、`top` 一类 PTY 程序；
- 调整浏览器窗口后同步 PTY 尺寸；
- 关闭页面后释放本次 SSH 会话。

MVP-0 不恢复断线前的终端会话。

### P0-04：与 Agent 对话

管理员可以针对一台设备创建 Agent Session，输入自然语言任务。Agent 能通过结构化 `remote_exec` 工具：

- 在 Remote 执行命令；
- 获得 stdout、stderr 和退出码；
- 根据结果继续执行后续命令；
- 在 WebUI 中流式展示文本、工具请求、执行状态和最终回答。

Agent 默认逐条请求命令授权。用户可以显式开启仅对当前 Agent Session 有效的 Full Access。

### P0-05：在线状态与自动重连

- Remote Client 每 5 秒发送一次心跳；
- Server 连续 15 秒未收到有效心跳，将设备标为 Offline；
- Remote Client 断线后自动指数退避重连；
- 重连成功后设备恢复 Online；
- 已断开的 Terminal 和正在执行的 Agent Tool Call 可以失败，不要求自动恢复。

## 4. 三天范围

### 必须有

- Linux Remote Client；
- Server 单体进程；
- 单管理员登录；
- Ed25519 设备身份和挑战签名；
- 一次性、短时效配对码；
- 设备列表与 Online/Offline；
- WSS 反向连接；
- 多路逻辑流；
- Embedded SSHD；
- Web Terminal；
- 一个 OpenCode Agent Provider 和可重复测试的 Fake Adapter；
- `remote_exec` 工具；
- Agent SSE 事件流；
- 命令确认与 Session Full Access；
- SQLite 持久化；
- Docker Compose/Caddy 示例部署；
- 至少一条自动化端到端冒烟测试。

### 明确不做

- Windows、macOS、Android 或 iOS Remote Client；
- 多用户注册、邀请、团队、RBAC；
- P2P、NAT Traversal、QUIC；
- 桌面画面、鼠标、键盘和剪贴板；
- 文件管理 UI、SFTP、SCP；
- 端口转发和 Web Preview；
- Terminal 重连与会话恢复；
- 多 Agent Provider、OpenCode 之外的 CLI Agent、通用 MCP、Skills；
- Agent 容器池和 Workspace 生命周期管理；
- 集群、Redis、消息队列、Kubernetes；
- 完整审计录像、命令策略 DSL；
- 自动升级、安装器、代码签名；
- SLA、灾备、水平扩容和生产安全认证。

## 5. 不可为赶工删除的底线

即使是三天 Demo，也必须满足：

- 公网流量使用 HTTPS/WSS；
- Server 身份由标准 TLS 证书验证；
- Device 私钥只保存在 Remote；
- Pairing Code 一次性且 10 分钟过期；
- 所有设备 API、Terminal 和 Agent 请求校验设备归属；
- Remote Client 拒绝以 root 身份启动，除非显式使用仅供开发的危险开关；
- Agent 命令有超时和输出上限；
- 默认逐条确认 Agent 命令；
- 不记录 Terminal 键盘输入；
- 日志和错误响应不输出密码、Cookie、设备私钥或 OpenAI API Key。

## 6. MVP-0 完成定义

在三台逻辑独立的主机/环境上完成以下演示：

```text
Remote Linux ──出站网络──► Server ◄──HTTPS── Browser
```

验收步骤：

1. 全新 Remote Client 首次启动并显示配对码；
2. 浏览器登录并成功配对；
3. 同一配对码二次提交失败；
4. 设备列表显示 Online；
5. Terminal 成功运行 `uname -a` 和一个交互式 PTY 程序；
6. 浏览器 resize 后远程 `stty size` 与页面尺寸一致；
7. Agent 接受“检查本机系统信息并总结”，请求并执行至少一次 `remote_exec`；
8. WebUI 显示工具命令、授权状态、退出码和 Agent 最终回答；
9. 断开 Remote 网络后 15 秒内显示 Offline；
10. 网络恢复后客户端自动重连并显示 Online；
11. 非设备所有者路径的访问在服务端被拒绝；
12. 一条 E2E 冒烟测试从配对覆盖到远程命令执行。

前 10 项是演示闭环；第 11、12 项是交付门槛。全部通过才称为 MVP-0 完成。

## 7. MVP-0 之后

MVP-0 通过后再进入 Alpha，优先级依次为：

1. 会话凭证轮换、Secret 管理、CSRF/限流完善；
2. Agent Runtime 容器隔离；
3. 断线恢复、背压、资源限制和故障注入测试；
4. 安装包、systemd、自动更新；
5. SFTP/文件能力和端口转发；
6. 多用户权限与完整审计；
7. 其他 CLI Agent、通用 MCP 和 Skills Adapter；
8. Desktop 辅助控制。
