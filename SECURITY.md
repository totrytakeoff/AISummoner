# Security Policy

AISummoner 是远程执行与 Agent 工具平台，安全边界属于核心产品功能。项目仍处于
Alpha，当前只支持最新 `main`；旧提交、个人测试部署和未签名构建不承诺安全支持。

## 报告漏洞

请不要在公开 Issue 中披露可利用细节、凭据、真实主机信息或日志内容。

优先通过 GitHub 仓库的 **Security → Report a vulnerability** 私下提交。若该入口
不可用，请只创建一条不含细节的 Issue 请求安全联系方式，等待维护者提供私密渠道。

报告建议包含：

- 受影响提交或版本；
- 最小复现条件和影响；
- 是否需要真实 Remote/Provider；
- 不含 secret 的日志分类或测试；
- 建议修复方向（如有）。

不要对非本人所有的部署进行扫描、压力测试或利用。

## 核心安全不变量

- Remote Client 只主动出站，不开放远程控制 TCP 端口。
- 生产控制面使用 TLS/WSS，不接受 insecure-skip-verify。
- Device identity、challenge、一次性配对和 owner predicate 不能绕过。
- Server 对 Remote SSH Host Key 做严格验证。
- Agent 命令默认逐次审批；Full Access 只作用于当前 Session。
- Runtime 不能选择 Device，也不能访问 Server 本地 shell/filesystem。
- 本地 GUI 只通过同 UID 私有 IPC 控制 daemon，不读取 Device 私钥。
- 密码、Cookie、API Key、私钥、配对码、Terminal 输入和命令输出不得进入日志。
- 关闭、取消、断线和 unpair 必须 joined 回收网络、PTY 和子进程。

更完整的威胁边界见
[协议、数据与安全基线](docs/baseline/02-protocol-data-security.md)。

## 非漏洞范围

以下通常属于当前 Alpha 限制，而不是单独的安全漏洞：

- 尚未支持多用户/RBAC、集群、Windows 或 macOS；
- 外部模型限流、计费、模型质量或 Provider 暂时不可用；
- 未签名 AppImage 的分发便利性；
- 测试部署地址过期或不可达；
- 尚未实现文件/桌面能力。

但如果这些限制导致越权、secret 泄漏、远程代码执行到错误主机、认证绕过或无法回收
受控进程，请按漏洞报告。
