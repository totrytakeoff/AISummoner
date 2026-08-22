---
task_id: task009
type: review
status: approved
from: task009_reviewer
to: orchestrator
revision: 2
decision: APPROVED
next_action: next_task
---

# Task 009 Review — Revision 2

## Decision

APPROVED

## Findings

No blocking issues found.

Revision 2 闭合了 revision 1 的 Caddy 共享来源键问题，同时保留了 revision 0/1 已验证的资源上界：

- `requestsource.Resolver` 只在 immediate peer 精确命中显式可信 IP 时读取专用头；可信请求必须只有一个规范 literal unicast IP。missing、repeated、comma-separated、whitespace/noncanonical、zone、unspecified、multicast、hostname 和 host:port 均 fail closed。
- 不可信 peer 完全忽略 dedicated、`X-Forwarded-For`、`Forwarded` 等来源声明，只使用规范化的直接 peer IP，不能通过伪造头绕开来源窗口。
- Login、已认证 Pairing Claim 和 Tunnel 都在各自 limiter/admission 之前调用同一个 Resolver；解析失败不会创建 limiter 项、消耗 Tunnel pre-auth slot 或进入 Login/Claim。
- Server composition 只构造一个 Resolver，并把同一变量注入 Browser API 与 Tunnel Gateway。standalone 构造器的 nil 默认仍是 direct-peer mode。
- Caddy 使用覆盖语义写入 `X-AISummoner-Client-IP {remote_host}`；Compose 用同一个可配置的精确 Caddy IPv4 同时定义其 edge endpoint 和 Server trust，只发布 Caddy 80/443。
- Config 只接受显式 literal trusted-proxy IP，拒绝 DNS、CIDR、zone、duplicate、空片段、unspecified 和 multicast，错误不回显被拒值。
- Tunnel 的 protocol-close/authenticated 日志不再包含 resolved source；Browser 的固定 400 envelope 和常规 request log 也不记录 header/source/config value。
- Revision 1 的 process-global 两槽 KDF gate，以及 Login/Claim/Tunnel 三个 4096 项 hard-cap、TTL/LRU、mutex 和无 per-key goroutine 约束均未退化。

## Reviewer Verification

- 完整重读 Task009 revision-2 plan、revision-1 review、revision-2 summary、16 个 C/D 修订文件及测试，并复核 Compose/Caddy、Server composition 和 Task010 实际拓扑。
- 独立 SHA-256 复核：16 个 revision-2 文件逐一匹配 summary 清单；post-manifest 的 Task010 `plan.md` 与 `preflight.md` 也分别匹配清单中的 `b4a0fa...d997` 和 `d90d25...099c`。
- 命令：`git diff --check`
  结果：PASS，无输出。
- 命令：`docker compose --env-file .env.example -f deploy/compose.yaml config --quiet`
  结果：PASS，exit 0，无校验输出。独立检查渲染拓扑确认默认 edge subnet、Server `.10`、Caddy `.20` 和 exact trust 插值一致。
- 未重复运行重型 Go/race/build。复核了 ready summary 的 fresh ASD merged-tree 证据：完整 `go test ./...`、22 个 internal race package、`go vet ./...`、Server/Client build 全部 PASS；资源门控约 5.45 GiB MemAvailable、2.06 GiB SwapFree，无残留 Go 进程。Fake/OpenCode 两种 Compose 校验也均为 exit 0 且 stdout/stderr 为零字节。
- 聚焦证据覆盖 trusted/untrusted peer、防伪、malformed-before-state、两来源 Login/Tunnel 隔离、source 日志哨兵、配置拒绝、root shared-resolver wiring，以及原 KDF/limiter race 与容量语义。首次夹具/传输/输出通道问题均未被伪装为通过，修正或机械重跑后有明确 PASS 结果。

## Residual Risks

- Task010 必须按已修订的 durable plan 动态证明实际 immediate peer：direct/Fake 阶段保持 trust 为空；TLS 阶段仅在 host-network Caddy peer 确认为 `127.0.0.1` 后设置该 exact trust，并验证 header overwrite、untrusted spoof、malformed trusted request 及两来源 Login/Tunnel 隔离。若实际 peer 不同，必须先解析单一精确地址，不能退化为 CIDR、DNS 或通用 forwarded header。
- Task008 的 Server Docker image build 仍只有 ASD 到 `proxy.golang.org` 超时这一环境残余。当前源码、直接 Go build 和 Compose 配置均通过，没有证据表明 clean image 必然失败；Task010 仍须在网络可用环境重试 image build，或按计划部署已验证的直接二进制。
- 自定义 Compose edge subnet/IP 与宿主既有网络冲突属于部署时环境校验项；默认静态拓扑已通过配置校验，Task010 使用独立且已明确的 host-network Caddy 拓扑。

## Next Action

- Task009 可以结束；继续 Task010 的 clean build、受控三主机部署与全链路验收。
