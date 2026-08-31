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

## Task023 当前证据（2026-08-31）

GitHub Actions `windows-2022` 已在同一原生作业中证明：Go 1.23 Windows
合同测试、Qt 6.8.3/MSVC Release 构建与 CTest、Qt `QLocalSocket` 到
`go-winio` 的真实 named-pipe 请求、DPAPI/ACL、suspended Job 子孙回收、
PowerShell UTF-8/stdout/stderr/exit/cwd，以及 ConPTY `101x37` resize、Ctrl-C 和重复
handle 清理。最终证据为
[run 33330465430](https://github.com/totrytakeoff/AISummoner/actions/runs/33330465430)，
详细失败/重试和产物 hash 见 `docs/agent_context/tasks/task023/summary.md`。

实测同时冻结了两个具体选择：Qt 名称
`LOCAL\AISummoner.Remote.v1` 对应 Go 路径
`\\.\pipe\LOCAL\AISummoner.Remote.v1`；peer 在读取协议前使用 exact pipe client
PID 的 process token/`TokenLogonSid` 并复核 PID，不使用依赖先读字节的
`ImpersonateNamedPipeClient`。ConPTY 子进程还必须显式置空父进程标准句柄，
防止已附着 console 的 host 绕过 pseudoconsole。

状态仍为 `Proposed`：目前的实机是 elevated Windows Server 2022 hosted runner，
尚缺普通桌面用户下的 Windows 11/Windows 10 22H2、第二 logon 拒绝、干净 VM、
GUI 启动生产 Core 的无 console flash/保活以及真实 Tunnel/Terminal 证据。

## Task024 当前证据（2026-08-31）

生产 Remote 已完成 common Core/platform backend 拆分：CLI runtime policy、Identity storage、
authenticated local IPC transport 和 SSH execution/session-process 均有明确接口与 Linux
build-tagged 实现；SSH wire/session、JSON dispatch、Ed25519 语义和 Remote 状态机保持共同。
Tunnel hello 只新增严格 `linux|windows` 枚举，未知值继续拒绝，协议版本不变。Linux 的真实
SSH 进程回收、IPC、Qt 与 AppImage 门通过，Windows Task023 probe 仍可交叉构建。

本证据只证明 Windows backend 有安全接入点。DPAPI/named pipe/token、PowerShell/Job 与
ConPTY 的生产 constructor 仍未实现，生产 Windows Client 仍不能构建/运行，因此不改变本
ADR 的 Proposed 状态或接受条件。详细命令、AppImage hash 与已知 DSH 测试夹具见
`docs/agent_context/tasks/task024/summary.md`。

## Task025 当前证据（2026-08-31）

生产 Windows Core 已实现 LocalAppData/普通交互 token policy、DPAPI CurrentUser + protected
ACL Device Identity，以及 frozen `LOCAL\\AISummoner.Remote.v1` 的 authenticated named-pipe
IPC。pipe 在读取 JSON 前核验 exact client PID 的 process-token `TokenLogonSid` 并复核 PID；
Identity 对 partial/corrupt/mismatch/DPAPI failure 均 fail closed。Task023 的 security/pipe
probe 已改为调用同一生产 helper，避免两套实现漂移。

最终证据为
[run 33359386282](https://github.com/totrytakeoff/AISummoner/actions/runs/33359386282)：
Windows Server 2022 原生测试、vet、真实生产 Core build、Qt/MSVC CTest、Qt→Go named-pipe
status/events 均通过，同时 Ubuntu 22.04 的现有 Core normal/race/vet 回归通过。artifact
`9746246672` 内的 unsigned 工程 ZIP 包含真实 `aisummoner-client.exe`，其 SHA-256 为
`7f2046f8f31f0f093e5d62827e082f5706c944c41708ca4b1baa5f97a11fb179`。

Task025 仍不改变本 ADR 的 Proposed 状态：Windows exec/shell 在生产 backend 中明确拒绝，
等待 Task026 PowerShell/Job 与 Task027 ConPTY；hosted runner 仍是 elevated Server 2022，
尚缺 ordinary-user Windows 11/10、第二 logon、clean VM、真实 Tunnel/Terminal/Agent 和签名
交付证据。Git Bash/MSYS2 不作为强制依赖；未来只能作为显式可选 Execution Profile。

## Task026 当前证据（2026-08-31）

生产 Windows SSH 非 PTY exec 已使用系统 Windows PowerShell 5.1、UTF-16LE
`-EncodedCommand`、显式标准 handle list、suspended CreateProcess 和 kill-on-close Job
Object。正常 leader 退出、context/channel 取消、Tunnel shutdown、TERM/KILL 都会终止并等待
完整 Job 与 I/O workers；非 PTY INT 明确拒绝，交互 shell 继续 fail closed。

最终证据为
[run 33361499043](https://github.com/totrytakeoff/AISummoner/actions/runs/33361499043)：
Windows Server 2022 上的真实 Device challenge、TLS/WSS、yamux、strict SSH client 与生产
Embedded SSHD 全链路通过，并证明 UTF-8 stdout/stderr、cwd、exit status、输出 drain、子孙
回收和无关进程不受影响；Windows vet/Core build、Qt CTest/IPC/打包与 Ubuntu normal/race/vet
回归也通过。artifact `9746897971` 内层工程 ZIP 的 SHA-256 为
`1dfc76ebec8fd81eb6d0811cccc26ad54c8518a98156d487f39712a5f7c3b9da`。

本证据仍不接受 ADR：生产 ConPTY、ordinary-user Windows 11/10、第二 logon、clean VM、GUI
无 console flash/保活、可信 Agent Execution Profile/真实 DSH Turn 和签名交付尚未完成。
Git Bash/MSYS2 仍未内置，也不是原生 PowerShell/ConPTY 的替代品。
