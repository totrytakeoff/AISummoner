# Windows 被控客户端移植方案

- 状态：Proposed
- 日期：2026-08-31
- 范围：Windows x86-64 Remote Core、Qt Desktop UI、Terminal、Agent 目标环境适配与交付
- 关联：ADR-0004、Task015、Task016、Proposed ADR-0007

## 结论

Windows 版不应复制一套独立客户端，也不能把当前 Linux 二进制直接交叉编译后视为完成。
推荐保留同一套 Go Remote Core、WSS/yamux Tunnel、配对、Device Identity 语义、严格
SSH、Qt 状态模型和本地 JSON IPC 协议，只在明确的平台边界下实现 Windows 后端：

```text
Qt Remote UI
   │  QLocalSocket
   │  Linux: Unix socket / Windows: named pipe
   ▼
Go Remote Core（共同状态机、配对、Tunnel、事件、IPC dispatch）
   ├─ identity storage
   │    Linux: 0600 PKCS#8   Windows: DPAPI(CurrentUser) + protected DACL
   ├─ local IPC transport
   │    Linux: Unix + SO_PEERCRED
   │    Windows: named pipe + logon-SID DACL + peer-token check
   └─ SSH session process backend
        Linux: PTY/pidfd/process-group
        Windows: PowerShell/ConPTY/Job Object
                  │
                  ▼
          既有 outbound-only WSS → yamux → strict SSH
```

首个可用 Windows 版本的产品流程仍是：双击 GUI，自动启动同目录后台 Core，显示设备码和
配对码；关闭 GUI 不断开后台连接；手动暂停会 joined 关闭 Tunnel 和全部子进程。用户不填
Server 地址，默认地址继续在构建时写入，设置页只保留高级覆盖入口。

## 实施进展（2026-08-31）

Task023 已在 Windows Server 2022 CI 证明 named pipe/peer token、DPAPI/ACL、PowerShell/
Job、ConPTY、Qt/MSVC 与工程打包合同。Task024 随后完成生产代码拆分：

- `internal/clientplatform`：平台名、数据目录、权限与 shutdown；
- `internal/identity`：共同 Ed25519 语义 + build-tagged storage；
- `internal/clientipc`：共同 JSON dispatch + authenticated transport/listener；
- `internal/sshserver`：共同 SSH Session 协议 + execution/session-process backend；
- Tunnel hello：严格 `linux|windows` 枚举，协议版本仍为 1。

Linux daemon、IPC、真实 SSH exec/PTY/进程树回收、Qt CTest 与 GUI+daemon AppImage 均已
回归；AppImage 打包也固定为最多两个 mksquashfs worker。Task025 现已把 Windows 普通用户
token/LocalAppData、DPAPI/ACL Identity 和认证 named pipe 接入这些生产 seam；真实
`aisummoner-client.exe`、Qt→Go IPC 与 Linux normal/race/vet 已在最终 CI 通过，并生成带
SHA-256 的 unsigned 工程 ZIP。Windows SSH exec/shell 仍明确拒绝，不用 no-op 冒充支持。
下一步是 Task026 的 PowerShell/Job Object exec，然后才是 Task027 ConPTY；Windows 仍未成为
支持平台，ADR-0007 仍为 Proposed。

## Task024 前代码审计

Tunnel、配对和大部分 Qt 页面可以复用，但以下位置把 Linux 行为写进了生产路径：

| 边界 | 当前状态 | Windows 所需变化 |
| --- | --- | --- |
| CLI/daemon | `os.Geteuid`、Unix signal、`~/.local/share` | 普通/提升令牌判定、Windows shutdown、LocalAppData |
| Tunnel hello | Client 和 Server 都只接受 `platform=linux` | 扩展为受限枚举 `linux/windows`，协议版本不变 |
| 本地 IPC | Unix socket、`0600`、`SO_PEERCRED`、inode 清理 | named pipe、显式 DACL、logon SID peer 验证 |
| Device 私钥 | 明文 PKCS#8 PEM + POSIX `0600` | DPAPI CurrentUser 密文 + 受保护 ACL + 原子写 |
| SSHD exec | `/bin/sh -lc`、process group、pidfd、`/proc` | Windows PowerShell、suspended CreateProcess、Job Object |
| SSHD PTY | `creack/pty`、Unix ioctl/signal | ConPTY、resize、Windows 取消语义 |
| Server cwd 校验 | Linux 主机上的 `filepath.IsAbs` | 按目标 Device platform 校验路径语法 |
| DSH Agent | 工具名/说明偏 Bash，未注入目标 OS/shell | 向 Runtime 提供可信 Execution Profile |
| Qt 启动器 | `unistd.h`、无 `.exe`、Unix 权限、AppImage cwd | Windows token、可信 sibling `.exe`、无控制台后台启动 |
| Qt IPC endpoint | 强制绝对路径且最长 100 字符 | endpoint 抽象；Windows 传 named-pipe server name |
| 交付 | Linux AppImage | MSVC x64、`windeployqt`、ZIP，随后签名安装包 |

因此，“能建立 Tunnel”和“Windows 全链路可用”是两个不同里程碑。尤其是 Agent：即使远端
能够执行 PowerShell，模型若仍被告知这是 Bash，也会稳定地产生错误命令。

## 支持基线

第一版建议只支持：

- Windows 11 x86-64 作为正式验收目标；
- Windows 10 22H2 x86-64 作为兼容性目标；
- 普通交互用户、桌面应用、同一登录会话；
- NTFS 本地用户目录；
- 内置 Windows PowerShell 5.1 作为固定首版 shell；
- Windows 10 1809 之前的系统、Windows Server Core、ARM64、MSIX/AppContainer、
  LocalSystem 服务和多用户共享 daemon 均不在首版范围。

ConPTY 的系统下限是 Windows 10 1809。Qt 当前 Windows 桌面支持同样覆盖 Windows 10
1809+ 和 Windows 11，因此这个范围不会为了旧系统引入第二套 PTY。

## 平台代码边界

不要给 `remoteclient.Controller` 或 IPC dispatch 填满 `if runtime.GOOS`。共同代码只依赖
小接口，具体实现通过 Go build tags 和 Qt `Q_OS_WIN` 隔离：

```text
internal/clientplatform/
  paths_linux.go          paths_windows.go
  privilege_linux.go      privilege_windows.go
  lifecycle_linux.go      lifecycle_windows.go

internal/clientipc/
  protocol.go             dispatch.go
  transport_unix.go       transport_windows.go

internal/identity/
  storage_unix.go         storage_windows.go

internal/sshserver/
  protocol.go             session.go
  process_linux.go        process_windows.go
  pty_linux.go            conpty_windows.go
```

原 `sshserver/server.go` 中的 SSH 握手、channel/request 解析、输入上限、exit-status 和
joined session 生命周期属于共同层；`/bin/sh`、PTY、signal、pidfd 和 `/proc` 扫描属于
Linux backend。Task024 已按此顺序完成拆分并保持 Linux 数据面回归，再启用严格 Windows
hello 枚举，没有一次性重写已经验证的数据面。

## Windows 运行与权限合同

### 普通用户后台进程，不做 LocalSystem 服务

首版沿用 Task015/016 的 Core daemon + GUI：GUI 通过 detached、无可见控制台的方式启动
`aisummoner-client.exe daemon ...`，named pipe 保证同一登录会话只有一个 Core。GUI 退出
不发送 pause；用户注销时进程随登录会话结束。

Windows Service 不适合首版：LocalSystem/Session 0 会让远程命令在错误的用户、profile、
桌面和权限上下文中执行；让 Service 以真实用户运行又需要凭据和额外生命周期管理。以后若
确实需要开机即连，应另做“系统 broker + 每用户 worker”的威胁模型，不能把当前 daemon
直接注册为 SYSTEM 服务。

### Windows 的“非 root”等价语义

- 允许 Administrators 组成员以正常的 UAC filtered token 运行；
- 拒绝 elevated/high-integrity token；
- 拒绝 LocalSystem、LocalService、NetworkService 和 Session 0；
- GUI 与 Core manifest 都使用 `asInvoker`，绝不请求自动提权；
- 错误文案明确提示“请普通启动，不要使用管理员身份运行”。

判定使用当前 process token 的 `TokenElevation`、`TokenIntegrityLevel`、`TokenUser` 和
session 信息，不能用“是否属于 Administrators 组”代替。

### 数据与设置位置

- Core 数据：`%LOCALAPPDATA%\AISummoner\RemoteClient`；
- Qt 非敏感偏好：继续使用 `QSettings` NativeFormat，即当前用户注册表；
- named pipe 不是数据目录内的伪文件；
- Windows 安装不会导入 Linux 私钥，每个 OS 安装是独立 Device，重新配对是预期行为。

Qt 和 Go 必须各自从 Windows Known Folder/`QStandardPaths` 得出同一个固定根，不能依赖
当前工作目录，也不能接受 GUI 自创的隐藏默认路径。

## 本地 IPC

### Endpoint

候选 endpoint 为登录会话作用域的：

```text
Go listener:  \\.\pipe\LOCAL\AISummoner.Remote.v1
Qt client:    LOCAL\AISummoner.Remote.v1
```

最终名称必须经过 Windows 实机互操作 spike 冻结。Qt 的 `QLocalSocket` 在 Windows 上原生
使用 named pipe，因此上层异步 poll/request 逻辑、64 KiB newline JSON v1、方法集合、超时
和最多 8 个 handler 均可直接保留。

### 认证与防护

Windows 默认 named-pipe ACL 会包含过宽主体，生产实现必须显式设置安全描述符：

- protected DACL 仅授予当前 logon SID 必需的 duplex 权限，不继承
  Everyone/Anonymous/Users/Administrators 权限；
- 设置拒绝远程 pipe client 的标志；
- 每次 accept 后从 exact pipe handle 读取 client PID，打开该进程 token 并比较
  `TokenLogonSid`，随后复核 pipe 的 client PID 未变化；不能在首字节到达前依赖
  `ImpersonateNamedPipeClient`，否则 Qt 异步连接会产生竞态；
- peer 验证失败时在读取 JSON 前关闭连接，只记录固定错误；
- listener 创建必须具有 first-instance/exclusive 语义，第二个 daemon 不接管已有 endpoint；
- 不以“pipe 名字很难猜”作为认证。

建议优先评估 Microsoft `go-winio` 作为 `net.Listener/net.Conn` carrier，继续使用已有
`golang.org/x/sys/windows` 完成 token、DACL 和 native-handle 验证。如果 carrier 不能安全
暴露 peer 验证所需 handle，则写一个很薄的私有 named-pipe listener，不降低为 DACL-only。

同一登录会话内的恶意同用户进程仍在信任边界内，这与 Linux 当前“同 UID 可调用 IPC”一致；
Windows 管理员/SYSTEM 也不属于本应用能够防御的本机完全失陷场景。

## Device Identity

Windows storage backend 保持同一 Ed25519 公钥、Device ID、challenge 签名和 SSH signer
语义，但不落明文 PEM：

1. 生成 PKCS#8 Ed25519 key；
2. 用 DPAPI `CryptProtectData`、CurrentUser scope、UI forbidden 保护；
3. 写入带 magic/version 的 `device_ed25519.dpapi`；
4. 数据目录和文件使用不继承的当前用户/SYSTEM ACL；
5. 同目录临时文件、flush、原子 replace，metadata 只有在 key 持久化成功后提交；
6. 读取时 DPAPI 解密并核对 metadata 的 Device ID，GUI 永远不参与；
7. key 丢失或 DPAPI 因账户恢复不可解密时 fail closed，明确要求 reset/re-pair，不生成一把
   新 key 覆盖现有 metadata。

DPAPI CurrentUser 的目的不是防御同用户恶意进程，而是避免离线复制明文私钥，并把密文绑定
到同一用户和机器。Device Identity 本来就应是机器安装级身份，因此不可跨机复制是可接受
结果。

## Windows 远程执行

### Shell 策略：原生优先，Git Bash 可选但不内置

首版继续以系统自带 Windows PowerShell 5.1 为唯一强制执行环境，不在客户端内捆绑 Git
Bash/MSYS2。Git Bash 可以减少一部分模型生成 Bash 命令时的适配工作，但不会消除 Windows
原生 token、DPAPI、Named Pipe、Job Object、ConPTY、路径和 GUI 生命周期问题；反而会立即
引入第二套路径转换/quoting、第三方组件许可清单、更新与漏洞响应以及明显更大的安装包。

正确的 Agent 兼容边界是 Server 派生的 Execution Profile，让模型明确知道目标是
`windows-powershell`，而不是用 POSIX 外观隐藏真实 OS。后续若用户机器已安装 Git for
Windows，可通过独立 task/能力协商增加显式 `git-bash` profile；不得自动探测后静默切换，
不得由 Browser 或模型自行声明，也不影响 PowerShell/ConPTY 作为原生保底路径。

### 共同 SSH 层

SSH channel 和 request 合同继续兼容：

- `exec` 返回独立 stdout/stderr 与 exit-status；
- `pty-req` + `shell` 返回终端字节流；
- `window-change` 映射到 PTY resize；
- channel/context 取消必须 joined 清理；
- command 8192 bytes、cwd 4096 bytes、PTY size、并发与时间上限不变。

### 非 PTY exec

首版命令语义冻结为 Windows PowerShell，而不是尝试猜 Bash/cmd/pwsh：

```text
powershell.exe -NoLogo -NoProfile -NonInteractive <robust encoded command>
```

命令必须用可靠的 UTF-16LE/`-EncodedCommand` 或等价无歧义传递，不能拼接 shell quoting。
进程创建时显式设置 working directory 和 UTF-8 输出合同。不存在 cwd 时使用用户 profile
目录，而不是 daemon/安装目录。

每个 SSH session 先创建带 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Job Object，再以
`CREATE_SUSPENDED` 创建根进程，成功 assign 后才 resume。这样避免根进程在被纳入 Job
之前抢跑并创建逃逸子进程。关闭、超时、Tunnel 断开和 pause 都终止并等待整个 Job，而不只
等待 PowerShell leader。

### ConPTY Terminal

交互 shell 使用同一个 Windows PowerShell，首版不自动切换用户机器上的 `pwsh`，避免
Terminal 和 Agent 命令方言漂移。实现顺序：

1. 建立两对同步 pipe；
2. `CreatePseudoConsole`；
3. 通过 `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` 创建 suspended shell；
4. assign Job Object 后 resume；
5. 独立 goroutine 持续 drain input/output，禁止单线程同步死锁；
6. `window-change` 调用 `ResizePseudoConsole`；
7. 清理时停止输入、终止 Job、等待进程和 copy goroutine、drain 尾帧，再关闭 ConPTY/pipe/
   Job handles。

PTY 内的 Ctrl-C 以终端字节 `0x03` 交给 ConPTY。非 PTY 的 SSH `INT/TERM/KILL` 不伪装成
Unix signal：首版可以将 TERM/KILL 明确定义为终止整个 Job；INT 只有在实机证明控制事件
可靠时支持，否则返回不支持，context cancellation 仍强制回收 Job。

## Server 与 Agent 的必要配套

Windows Remote 上线需要两处受控的服务端改动，但不重写 Server：

### 目标感知的路径校验

当前 Server 在 Linux 上使用 `filepath.IsAbs` 校验 Agent `cwd`，所以 `C:\work` 会在到达
Windows 前被拒绝。应增加一个小的 `pathsyntax`/Execution Profile 层：

- parser 只做 UTF-8、长度、NUL 和 schema 校验；
- 根据 owned Device 的 `platform` 判断 POSIX 或 Windows absolute path；
- `sshclient.ExecOptions` 携带已经从 Device Registry 得到的 platform；
- Remote SSHD 再按本机平台做最终 cwd 校验和目录存在性检查；
- Browser/模型不能自行声明 platform。

### Agent Execution Profile

Device Registry 已保存可信 `platform/arch`，应派生并传给 Runtime：

```json
{
  "platform": "windows",
  "arch": "amd64",
  "shell": "windows-powershell",
  "path_flavor": "windows"
}
```

DSH 的 Remote tool 目前名为 `bash` 且说明只表示“远端命令”，没有目标 OS/shell 信息。Windows
Agent 放行前，DSH Adapter 必须把上述 server-owned profile 注入当前 Runtime Session，并把
工具展示/说明改为平台中性；模型不得从用户文本猜 OS。未来 OpenCode/Codex/Claude 共用同一
profile seam。

首个 Windows 工程包可以先验收 GUI、配对和 Terminal，但不能在 Agent profile 尚未完成时
宣传“Windows Agent 可用”。最终 Windows Alpha gate 必须包含真实 DSH → PowerShell →
Windows Device 的一轮安全命令。

## Tunnel 与公共协议

- WSS、TLS、Device challenge、配对、owner、yamux、strict SSH Host Key 和 stream header
  不变；
- hello 的既有 `platform` 字段从固定 `linux` 改为严格枚举 `linux|windows`；
- Client 使用 `runtime.GOOS` 映射后的受限值，Server 拒绝未知值；
- 仅增加 Windows 不需要升级 Tunnel protocol version；
- 首版 shell 固定由 platform 派生，不急于给 hello 增加未验证的 capability bag；
- Web 已把 platform 当字符串展示，只需补测试和 Windows 友好文案。

## Qt GUI 移植

大部分 Widgets、主题、状态/事件模型和异步 IPC 可复用。平台改动集中在：

- `main.cpp`：移除无条件 `unistd.h/geteuid`，调用平台 privilege check；
- `AppSettings`：Linux 路径保持不变，Windows 使用固定 LocalAppData root；
- `DaemonClient`：参数从 `socketPath` 抽象为 `localEndpoint`，只在 Unix backend 要求绝对
  路径/100 字符；
- `DaemonLauncher`：Windows 查找 `aisummoner-client.exe`，拒绝目录/reparse 异常，使用
  detached + no-window 方式启动；
- CMake：`WIN32` executable、icon/resource/version、`asInvoker` manifest、安装/部署脚本；
- 测试：named-pipe fake daemon、自动启动、GUI 关闭后 Core 存活、提升运行拒绝。

Windows GUI 仍只显示状态、配对、事件和暂停控制，不读取私钥，也不新开 TCP 端口。

## 构建与交付

第一阶段在 Windows CI/开发机原生构建，避免把交叉编译成功误当运行证明：

- Go 1.23，`CGO_ENABLED=0`，`windows/amd64`；
- Qt 6 Widgets/Network，MSVC 2022 x64；
- `windeployqt` 收集 Qt DLL、platform/style 插件和 compiler runtime 合同；
- 工程测试包为 portable ZIP，包含 GUI、Core、Qt runtime、licenses、SHA-256 和启动说明；
- 在无 Qt/Go 开发环境的干净 Windows VM 上验证；
- 产品交付再增加 per-user installer、开始菜单/卸载和可选登录启动；首版不使用 MSIX，避免
  在 IPC、后台进程和升级语义尚未验证前叠加 package-container 约束；
- 发布前对 GUI、Core 和 installer 做 Authenticode 签名和可信时间戳。

“没有代码签名证书”不阻塞内部 ZIP 测试，但阻塞公开稳定版声明。

## 威胁模型

| 威胁 | 控制 | 首版剩余风险 |
| --- | --- | --- |
| 网络 MITM/假 Server | 既有 HTTPS/WSS、challenge、strict SSH key | 用户/系统信任库失陷不在应用防线内 |
| 其他本机用户读私钥 | protected ACL + DPAPI CurrentUser | 管理员/SYSTEM 可接管机器 |
| 其他登录会话调用 GUI IPC | `LOCAL` scope、logon-SID DACL、peer-token check | 同一 logon session 的同用户进程受信 |
| 恶意进程抢占 pipe | exclusive first instance、peer check、固定失败 | 同用户恶意进程仍可造成拒绝服务 |
| elevated daemon 扩大远控权限 | `asInvoker` + elevated/high-integrity 拒绝 | 用户主动削弱 OS 策略不在防线内 |
| 子进程在取消后残留 | suspended assign + kill-on-close Job + joined wait | 第三方驱动/系统进程不受普通 Job 控制 |
| ConPTY handle 泄漏/死锁 | 双向独立 drain、严格 handle ownership、超时测试 | Windows/终端实现缺陷需版本测试 |
| Agent 生成 Linux 命令 | server-owned Execution Profile、PowerShell 工具语义 | 模型仍可能犯错，审批/exit code 保留 |
| 路径逃逸/不同语法误判 | target-aware absolute path + Remote 二次校验 | 授权用户仍可访问其 OS 权限允许的路径 |
| 被篡改的 sibling daemon | 安装目录/签名检查、同目录 canonical/reparse 校验 | portable ZIP 位于用户可写目录，等同 GUI 本体被篡改 |

## 分阶段实施

### Task023：Windows 兼容性与安全 spike

只证明关键 API 和互操作，不启用生产 Windows hello：Go↔Qt named pipe、logon SID peer
认证、DPAPI/ACL、suspended Job、非 PTY PowerShell、ConPTY resize/cleanup、GUI 无窗口
detached 启动和 Windows CI toolchain。根据实测修订并接受/拒绝 ADR-0007。

### Task024：共同 Core 平台拆分

把 paths/privilege/identity/IPC/process 接口从 Linux 实现中抽出，使用 build tags，保持 Linux
AppImage、daemon、Terminal 和 Agent 回归全绿；扩展 hello platform enum 和测试。
**实现完成，等待独立 review；不代表 Windows backend 已完成。**

### Task025：Windows Core、Identity 与 IPC

完成 LocalAppData、普通 token gate、DPAPI identity、named pipe IPC、single instance 和
daemon joined lifecycle。此时 CLI/status 可在 Windows 工作，但还不宣称 Terminal 可用。
实现期间使用明确拒绝 exec/shell 的 Windows SSH backend 让 Core 可构建；它不是成功返回的
占位实现。Git Bash 不进入本任务依赖。
**实现完成，最终 Windows/Linux CI 与工程 ZIP 证据已移交人工 review；不代表 Terminal、
Agent 或 Windows 正式支持。**

### Task026：Windows exec 与 Job Object

完成 PowerShell 非 PTY exec、cwd、stdout/stderr/exit、timeout/cancel 和完整进程树回收，并
通过真实 WSS/yamux/SSH 链路；这是 Agent transport 的底座。

### Task027：ConPTY Terminal

完成 shell、UTF-8/VT、resize、Ctrl-C、断连/pause cleanup 和 Browser xterm 实机测试。

### Task028：Qt GUI 与工程 ZIP

完成 Windows UI、自动/无窗口 daemon 启动、关闭 GUI 保活、`windeployqt` portable ZIP 和
干净 VM 验证。

### Task029：Agent 平台适配与发布门

完成 target-aware cwd、Execution Profile、DSH PowerShell 语义、真实 Agent Turn；再做
per-user installer、签名、Windows 11/10 E2E、升级/卸载/回滚说明。

每个 Task 都必须保持 Linux 现有行为，不得等到最后一次性修复回归。

## 验收矩阵

- 构建：Linux client/server/web 全绿；Windows Go/Qt Release build 全绿；无 CGO；
- 身份：首次生成、重启稳定、ACL/DPAPI、损坏 fail closed、GUI 不可读取；
- IPC：Qt↔Go、并发/超限/超时、其他 logon 拒绝、second daemon 拒绝、崩溃后可恢复；
- lifecycle：GUI close 后 daemon 在线，pause/resume joined，无 console flash，无 elevated run；
- exec：PowerShell quoting、Windows cwd、UTF-8 中文、stdout/stderr、exit code、timeout；
- Terminal：xterm 输入输出、resize、Ctrl-C、断网重连、进程树无残留；
- Agent：DSH 明确感知 Windows/PowerShell，审批正确，一轮 `Get-Location` 类 benign Turn；
- 安全：TLS/owner/SSH/pairing/approval/limits/redaction 保持，Remote 仍无 TCP listener；
- 交付：干净 Windows 11 VM 双击可用；Windows 10 22H2 兼容 smoke；hash/rollback 完整。

## 实施前必须由 spike 回答的问题

1. Qt `QLocalSocket` 与所选 Go named-pipe carrier 的 exact endpoint/byte-stream 互操作是否稳定；
2. carrier 是否能在 accept 后安全获得 native handle 做 logon SID peer 验证；
3. `QProcess` 是否能无窗口 detached 启动 console-subsystem Core，还是需要 Windows native
   launcher/helper；
4. Windows PowerShell 非 PTY 的 UTF-8、stderr 和 exit-status 如何在中英文系统上稳定；
5. ConPTY + suspended process + Job Object 的创建/清理顺序是否在 Windows 10/11 都无泄漏；
6. Windows CI 使用的 Qt 精确版本、compiler runtime 与最终最低系统版本；
7. portable ZIP 通过后，per-user installer 选择 WiX、NSIS 或其他方案。

这些问题不改变总体架构，但会改变具体实现和依赖，因此 ADR 在 spike 前保持 Proposed。

## 主要参考

- [Qt QLocalSocket：Windows 上使用 named pipe](https://doc.qt.io/qt-6/qlocalsocket.html)
- [Microsoft：Named Pipe Security and Access Rights](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights)
- [Microsoft：GetNamedPipeClientProcessId](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeclientprocessid)
- [Microsoft：Creating a Pseudoconsole Session](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session)
- [Microsoft：Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)
- [Microsoft：DPAPI CryptProtectData 示例与 scope](https://learn.microsoft.com/en-us/windows/win32/seccrypto/example-c-program-using-cryptprotectdata)
- [Qt Windows Deployment / windeployqt](https://doc.qt.io/qt-6/windows-deployment.html)
- [Microsoft SignTool](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool)
- [Microsoft go-winio](https://github.com/microsoft/go-winio)
