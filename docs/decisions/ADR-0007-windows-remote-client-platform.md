# ADR-0007：Windows 被控客户端平台边界

- 状态：Proposed
- 日期：2026-08-31
- 关联：ADR-0001、ADR-0004、Task015、Task016、Task023
- 详细设计：`docs/design/windows-remote-client-port.md`

## 背景

当前 Remote Client 已形成 Go Core daemon + Qt Desktop UI，并在 Linux 上证明了 Device
Identity、出站 Tunnel、严格 SSH、Terminal、Agent Remote Exec、本地 IPC、暂停/恢复和 GUI
关闭后后台保活。Windows 是新的 OS 安全和进程边界，不是编译目标开关：Unix socket/
`SO_PEERCRED`、POSIX mode、`/bin/sh`、PTY、pidfd、`/proc`、signal、root gate、AppImage 和
Linux 路径语法都没有直接的 Windows 等价物。

若复制一套 Windows Client，会让 challenge、配对、Tunnel、SSH 与状态机逐渐分叉；若只做
交叉编译，则会得到无法安全 IPC、无法交互 Terminal、无法清理子进程且 Agent 命令方言错误
的伪移植。

## 提议决策

### 1. 共同 Core + 平台 backend，不 fork 产品

`remoteclient.Controller`、Tunnel、Device/Pairing、SSH channel protocol、IPC JSON dispatch、
Qt 页面/状态模型继续共同维护。paths、privilege、identity storage、IPC transport 和 SSH
process/PTY 使用小接口与 build tags/Qt platform guards 分离。Linux 现有实现保持第一等公民。

### 2. Windows 运行在普通用户登录会话

首版是 GUI 自动启动的 per-user 后台 Core，而不是 LocalSystem Windows Service。允许管理员
组成员使用未提升 token，拒绝 elevated/high-integrity、系统账户和 Session 0；manifest 为
`asInvoker`。关闭 GUI 不停止 Core，pause 才 joined 关闭远程会话。

### 3. 本地 IPC 使用登录会话作用域 named pipe

IPC v1 schema、方法和上限不变。Windows transport 使用 named pipe，必须具有 protected
logon-SID DACL、remote-client rejection、exclusive first instance，并在 accept 后比较 peer
token 的 logon SID。peer token 由 exact pipe handle 的 client PID 定位，打开进程 token 后
再次复核该 pipe client PID，且在读取协议字节前完成；不使用依赖“已读取上一条消息”的
`ImpersonateNamedPipeClient`。默认 pipe ACL 或仅靠不可猜名称不可接受。Qt 继续通过
`QLocalSocket` 异步通信。

### 4. Windows Device key 使用 DPAPI CurrentUser

Ed25519/Device ID/SSH signer 语义不变。私钥以 DPAPI CurrentUser blob 写入固定 LocalAppData
目录，同时使用当前用户/SYSTEM protected ACL 和原子提交。GUI 不接触私钥；损坏或不可解密
时 fail closed，不静默轮换身份。Linux PEM `0600` 格式不改变，也不自动跨 OS 迁移身份。

### 5. Windows 进程由 Job Object 完整托管

非 PTY exec 固定使用内置 Windows PowerShell；PTY 使用 ConPTY。每个 SSH session 先创建
kill-on-close Job，再 suspended 创建根进程，assign 成功后 resume。取消、超时、断连和 pause
终止并等待整个 Job、I/O goroutine、ConPTY 和 handles，不能只 kill leader。

### 6. Server 派生可信目标执行环境

Tunnel hello 的现有 platform 字段扩展为严格 `linux|windows`，不升级协议版本。Server 从
owned Device 派生 path flavor 和 shell profile，修复在 Linux Server 上拒绝 Windows cwd 的
校验，并把 `windows-powershell` 环境传给 DSH/未来 Runtime。Browser 和模型不能声明目标 OS。

### 7. Windows 先发工程 ZIP，再做签名安装包

使用 Windows 原生 CI、Go `CGO_ENABLED=0`、Qt/MSVC x64 和 `windeployqt`。第一份测试资产是
portable ZIP；干净 VM 和真实链路通过后，再选择 per-user installer 并对 GUI/Core/installer
做 Authenticode 签名。首版不使用 MSIX/AppContainer。

## 保持不变的安全不变量

- Remote 仍只建立出站 WSS，不开放 TCP/局域网监听；
- TLS、challenge、一次性配对、owner predicate、strict SSH Host Key、Agent approval、上限、
  redaction 和 joined shutdown 不放宽；
- GUI 不能读取 Device 私钥；
- Windows backend 只替换 OS primitive，不新增绕过 Server authority 的本地/远程能力。

## 后果

正面：同一协议和产品状态机覆盖 Linux/Windows；Windows Terminal 能使用原生 VT/ConPTY；
进程树和本地 IPC 有明确的 OS 安全等价物；未来 macOS 可复用相同平台 seam。

代价：在产出可测试 GUI 包之前必须先做 named-pipe/ConPTY/Job/DPAPI spike；`sshserver` 需要
拆分共同协议和平台进程层；Agent Runtime 需要目标环境描述，不是单纯客户端改动；Windows
发布需要原生 CI、runtime bundling 和代码签名。

## 被拒绝的方案

- **完整复制 Windows Client**：会复制并漂移关键安全和生命周期代码；
- **要求 WSL/Git Bash**：增加外部安装前置，不能代表原生 Windows，被控权限也更难解释；
- **首版直接注册 LocalSystem Service**：远程命令会在错误用户/Session 0 权限上下文运行；
- **只依赖默认 named-pipe ACL**：默认主体过宽，弱于现有 same-UID Unix IPC；
- **只 kill PowerShell PID**：子孙进程可在断开后残留；
- **把所有 Windows signal 假装成 POSIX signal**：语义错误且会给调用方虚假保证；
- **先上 MSIX**：在 core IPC/lifecycle 未证明前引入 package-container 变量；
- **宣布 Tunnel 连接成功即移植完成**：Terminal 与 Agent 仍可能不可用或使用错误 shell。

## 接受条件

本 ADR 只有在 Task023 于 Windows 10/11 实机证明 Qt↔Go named pipe 与 peer authentication、
DPAPI/ACL、suspended Job、PowerShell exec、ConPTY resize/joined cleanup 和无窗口 daemon 启动
后，才可改为 Accepted。Spike 若推翻具体 carrier/endpoint/launcher，只修订实现选择，不默认
放宽上述安全合同。
