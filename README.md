# AISummoner

AISummoner 是一个以服务端 AI Agent 和 SSH 为核心的远程执行平台：被控端主动连接服务端，用户从浏览器完成设备配对、远程终端操作和 Agent 对话。

MVP-0 已完成真实纵向链路验证。当前进入 Alpha 规划与重构阶段：保留已经验证的
远程安全底座，重做 Browser Controller 的工作区/Agent 交互，并把命令行 Remote
Client 演进为带独立守护核心的桌面客户端。

## 当前基线

- 已完成基线：`MVP-0 / Baseline 0.1`
- 当前方向：`Alpha / Direction 0.1`
- Alpha 方向日期：`2026-08-23`
- 支持平台：Linux Remote Client
- 部署模型：单节点、单管理员、自托管
- 交付口径：功能闭环 Demo，不承诺公网生产可用性

## Alpha 重构方向（2026-08-23）

MVP 证明了链路，不代表当前两个客户端已经是最终产品。后续主线固定为：

```text
登录 → 选择或绑定 Device → Control Workspace

Control Workspace:
  左侧 Session
  中间 Agent Conversation
  右侧可选 Terminal / Device Activity / 未来 Desktop
```

Controller 的 Agent 体验以 DSH 为主要交互参考，并采用类似 VS Code/Zed 的
可调整工作区；不会直接 fork/iframe DSH，也不会引入第二套 Session、权限或本地
执行后端。丰富 Runtime 适配顺序为 `DSH → OpenCode → Codex → Claude Code`，
直接 DeepSeek 继续作为轻量模型 API Adapter。

Remote Client 已演进为 `Go daemon + 私有本地 IPC + Qt Desktop UI`：GUI 提供
配对码与过期刷新、连接/被控状态、脱敏活动记录、手动暂停/恢复；关闭 GUI 不会关闭
后台 Tunnel。后续本机权限只能进一步收紧 Server 授权。

完整产品与架构方向见
[Alpha 产品与双客户端重构方向](docs/baseline/04-alpha-product-direction.md)和
[ADR-0004](docs/decisions/ADR-0004-alpha-clients-and-agent-runtime.md)。Remote Core
由 [Task015](docs/agent_context/tasks/task015/plan.md) 实现；Qt 6 Widgets GUI 与
Ubuntu 兼容 AppImage 由 [Task016](docs/agent_context/tasks/task016/plan.md) 实现，
界面规格见 [Qt GUI 设计](docs/design/remote-client-qt.md)。

## MVP-0 验收状态（2026-08-13）

三台逻辑主机上的 Baseline-0 功能闭环已经通过：真实 Linux Remote
Client、Browser Terminal/PTY/resize、Fake Agent 审批与生命周期、以及固定
OpenCode 1.18.11 的真实 `remote_exec` 都有自动化证据。严格 TLS/WSS/SSE
也通过了专用测试 CA 与 scoped loopback forwarding；生产 Client 未使用
`--dev` 或 insecure-skip-verify。

当前 Task011 测试部署在用户授权的 ASD TCP 10001 上使用公开受信任的
Let's Encrypt 短期 IP-address 证书，Browser 与生产 Client 都直接访问
`https://122.51.70.33:10001`，不安装私有 CA、不设置 `SSL_CERT_FILE`，也
不使用 `--dev` 或 insecure TLS。Browser 已完成 HTTPS/Secure Cookie、
Terminal WSS 与 Agent SSE；80/nginx、10002 以及全部既有容器保持不变，
8088/14096/14097 未向公网暴露。完整 Task011 部署与续期证据见
[当前测试部署记录](docs/acceptance/task011-test-deployment-2026-08-21.md)；
原三机 12 项 MVP 矩阵仍见
[MVP-0 验收记录](docs/acceptance/mvp-0-2026-08-13.md)。

完整闭环：

```text
启动 Remote Client
  → 获得一次性配对码
  → WebUI 绑定设备
  → 打开交互式终端
  → 发起 Agent 对话
  → Agent 通过 SSH 在 Remote 执行命令
  → WebUI 展示命令、状态和结论
```

## 基准文档

实现、测试和验收以以下文档为准：

1. [编码代理入口与约束](AGENTS.md)
2. [产品与范围基线](docs/baseline/00-product-scope.md)
3. [架构与技术栈基线](docs/baseline/01-architecture-stack.md)
4. [协议、数据与安全基线](docs/baseline/02-protocol-data-security.md)
5. [三天 MVP 实施与验收计划](docs/baseline/03-mvp-plan.md)
6. [ADR-0001：MVP 技术决策](docs/decisions/ADR-0001-mvp-stack.md)
7. [ADR-0002：OpenCode Agent Runtime](docs/decisions/ADR-0002-opencode-runtime.md)
8. [ADR-0003：统一 Agent 调用与 Web 交互适配层](docs/decisions/ADR-0003-agent-adapter-ui.md)
9. [Alpha 产品与双客户端重构方向](docs/baseline/04-alpha-product-direction.md)
10. [ADR-0004：Alpha 双客户端与 Agent Runtime 架构](docs/decisions/ADR-0004-alpha-clients-and-agent-runtime.md)

[《RemoteAgent 项目初步设计说明书》](RemoteAgent_项目初步设计说明书.md)保留为项目愿景和前期思考记录，不再作为 MVP 实现细节的唯一依据。

## 文档权威顺序

发生冲突时，按以下顺序处理：

```text
已接受的 ADR
  > docs/baseline 当前版本
  > README
  > RemoteAgent_项目初步设计说明书.md
```

任何改变 MVP 范围、信任边界、协议兼容性或核心技术栈的决定，都应先新增或修改 ADR，再同步更新 baseline。一般实现细节可直接通过代码和测试演进。

## 名词约定

- **AISummoner**：产品与代码仓库名称。
- **Remote Client**：运行在被控 Linux 机器上的常驻客户端。
- **Server**：控制平面、Tunnel Gateway、Terminal Gateway 和 Agent Runtime 的单体服务。
- **Controller**：浏览器中的 WebUI。
- **RemoteAgent**：原始设计文档中的项目名；在新文档中泛指远程执行子系统。
- **MVP-0**：三天内完成的功能闭环 Demo。
- **Alpha**：在 MVP-0 已验证底座上，重构两个客户端、Agent Runtime 兼容层、
  交互与可运维性的可试用版本。

## 本地启动（Fake Agent）

本地开发不需要 OpenCode 凭据。复制示例配置，为三个敏感字段生成互不相同的随机值，然后启动 Server：

```bash
install -m 600 .env.example .env
openssl rand -base64 48
openssl rand -base64 48

set -a
. ./.env
set +a
make run
```

首次启动时 `AISUMMONER_ADMIN_PASSWORD` 用于创建唯一管理员；创建成功后应从环境文件删除。开发构建会嵌入一个明确标识的占位页面。完整 WebUI 由生产 Docker 构建先执行锁文件固定的 `npm ci`、前端测试与 Vite build，再嵌入 Go Server。

`AISUMMONER_SESSION_SECRET` 仍按冻结的 Baseline-0 最小配置做不少于 32 字节的启动校验，但它是当前版本的兼容预留项，不参与 Web Session 派生或验签。实际 Session Token 由密码学随机数独立生成，SQLite 只保存其 SHA-256 digest。不要把该预留字段误当成当前 Session 安全性的来源；它仍应使用随机值并避免记录，为后续凭证轮换兼容保留。

Remote Client 默认拒绝 root，并默认只接受标准 TLS：

```bash
./aisummoner-client start --server https://aisummoner.example.com
```

`start` 是保留的交互式兼容模式，配对码只写 stdout。常驻模式使用独立 Remote
Core 和私有本地 IPC，不会把配对码写到 stdout/journal：

```bash
./aisummoner-client daemon \
  --server https://aisummoner.example.com \
  --data-dir "$HOME/.local/share/aisummoner"

./aisummoner-client status --data-dir "$HOME/.local/share/aisummoner"
./aisummoner-client pause --data-dir "$HOME/.local/share/aisummoner"
./aisummoner-client resume --data-dir "$HOME/.local/share/aisummoner"
./aisummoner-client refresh-pairing --data-dir "$HOME/.local/share/aisummoner"
```

这些控制命令只连接 data directory 内 mode `0600` 的 Unix socket；daemon 会在
读取请求前核对 Linux `SO_PEERCRED`，只接受同 UID peer。`status` 是唯一会返回
当前有效配对码的方法；`events.list` 及 daemon 日志不会包含配对码、Server URL、
命令、终端/Agent 内容或 SSH 材料。该 socket 是本机 GUI/CLI API，不是远程 API。
完整 v1 schema 见 [Remote Client Private IPC v1](docs/design/remote-client-ipc-v1.md)。

只有 loopback 明文开发环境可以显式加 `--dev`。root 开发还必须同时显式加 `--allow-root-dev`，生产环境不要使用这两个开关。

本地直连开发应保持 `AISUMMONER_TRUSTED_PROXY_IPS` 为空。只有 Server 前面确实
存在受控反向代理时，才把其直接 peer 的精确数值 IP 列入该变量；不接受 hostname、
CIDR 或整段子网。Server 只对精确匹配的 peer 读取专用
`X-AISummoner-Client-IP`，其他直连请求携带的所有代理头都会被忽略。

## Agent 运行模式

- `AISUMMONER_AGENT_ADAPTER=fake`：确定性 MVP/测试模式；不读取、不验证、也不启动 OpenCode 或 Bridge。
- `AISUMMONER_AGENT_ADAPTER=deepseek`：直接使用 DeepSeek Chat-Completions 流式 API；要求 HTTPS origin、全新轮换的 API Key 和显式模型。该模式不启动 OpenCode/Bridge，Provider 仍只能通过 AISummoner 的 `remote_exec` 审批边界操作选中的 Remote。
- `AISUMMONER_AGENT_ADAPTER=opencode`：要求数值 loopback OpenCode URL、Basic Auth 用户名/密码、模型、私有 workspace、独立 loopback Bridge listener、精确 callback URL 和至少 32 字节的 Bridge secret。

Go Server 的 Provider 调用层与 Web presentation adapter 相互独立。Provider 原生事件必须先在 Server 归一化；Web 再把用户消息、折叠的 reasoning、最终回答和工具调用按事件顺序投影到同一 timeline，并按 Provider/工具选择展示器。DeepSeek 和 OpenCode 是真实 Provider；Fake 只用于确定性链路测试且会在 UI 中明确标识。交互职责参考 DSH 的持久会话、折叠思考与审批接管，但不引入其本地 shell、文件系统或插件后端。扩展规则见 [ADR-0003](docs/decisions/ADR-0003-agent-adapter-ui.md)。

进入 Agent 页面时，有历史会话就直接恢复；无历史且 Device 在线时自动创建默认 `per_command` 会话，不再先弹“选择模式”向导。`New conversation` 也直接创建新的逐命令确认会话。只有在某个真实待审批命令上显式确认“Approve session”，才会把当前会话提升为 Full Access；权限不会跨会话保留。

交互测试不必登录 Server shell 写环境文件。管理员登录 Web 后，可以在 Agent
页选择 `Set up DeepSeek`，粘贴 Key 并直接开始一个新的 DeepSeek 对话。Key
只通过当前同源 HTTPS 请求进入 Server 进程内存：不写 SQLite、日志、审计、
响应、URL 或浏览器持久存储；取消或提交成功后表单即清空。Server 重启后需要
重新填写。模型已有默认值，通常只需粘贴 Key。无人值守部署仍可使用下方环境
变量方式启动。

DeepSeek 与 OpenCode 是两个可替换的 Server-side Adapter，不是两套产品后端。认证、Device owner、审批、Tunnel/SSH、超时/输出上限、持久会话与 Web 事件始终只由 AISummoner 掌控。不要把 DSH 的 credential/session 目录复制到 Server；真实部署只注入新轮换的 DeepSeek Key。

OpenCode 与 Bridge 都不得发布端口。部署示例使用同一 Docker network namespace，使二者能够通过 `127.0.0.1` 通信；OpenCode 只会在每个空 workspace 中看到 deny-by-default 策略和 `remote_exec` 工具。模型可用性不属于公共 `/healthz`：外部限流或不可用会在 Turn 中如实失败，不会回退到 Server 本地 shell。

OpenCode 模式在开放公共 readiness 前会在一个有总上限的启动窗口内探测 sidecar。Compose 的 `condition: service_started` 先创建 Server 容器及其 network namespace，再允许 sidecar 在 Server 尚处于 bounded health wait、public Runtime 尚未 serving 的阶段启动；短暂连接不可用会重试，健康后才接受业务流量。HTTP 429 按 `rate_limited` 分类并 fail-closed，不把凭据、URL 或响应体放入错误。若首次窗口仍未收敛，Server 的 `unless-stopped` 策略会重启并再次进行同一有界探测。公共 `/healthz` 始终只表示 Server 与 SQLite 状态。

## Compose + Caddy 示例

1. 使用 `install -m 600 .env.example .env` 创建仅当前用户可读写的 `.env`，把 hostname、bootstrap 密码和当前 Adapter 所需的 secret 替换为真实安全值；启动前用 `stat -c '%a' .env` 确认权限为 `600`。
2. OpenCode 模式由 [deploy/OpenCode.Dockerfile](deploy/OpenCode.Dockerfile) 从固定 Node 基础镜像安装精确的 `opencode-ai@1.18.11`，构建时断言版本，并只使用固定本地镜像名 `aisummoner-opencode:1.18.11`。MVP 不提供未经同一构建合同验证的镜像覆盖。Fake 模式必须保持 `AISUMMONER_AGENT_ADAPTER=fake` 且 `COMPOSE_PROFILES` 为空；DeepSeek 模式设置 `AISUMMONER_AGENT_ADAPTER=deepseek`、全新 API Key 和模型，同时保持 `COMPOSE_PROFILES` 为空；OpenCode 模式必须同时设置 `AISUMMONER_AGENT_ADAPTER=opencode` 和 `COMPOSE_PROFILES=opencode`。校验脚本会拒绝不一致组合；Fake 模式不读取或校验任何 Provider 专用设置。
3. 确保 DNS 指向宿主机，并只开放 Caddy 的 80/443。
4. 验证并启动：

```bash
test "$(stat -c '%a' .env)" = 600
sh deploy/validate-compose.sh
docker compose --env-file .env -f deploy/compose.yaml up -d --build

# DeepSeek 模式保持 COMPOSE_PROFILES 为空；OpenCode 模式设为 opencode。
```

`validate-compose.sh` 固定使用 `docker compose config --quiet`，只通过退出状态报告配置是否有效；它拒绝额外参数，也不会把插值后的密码或 secret 渲染到终端/CI 日志。拓扑内容审查只针对仓库中的静态 `compose.yaml` 与无真实 secret 的 `.env.example`。

拓扑约束：

- 只有 Caddy 发布 HTTP(S)；Server、OpenCode 和 Bridge 均不发布宿主端口。
- `edge` 网络、Server 与 Caddy 使用可配置但确定的私有 IPv4；Server 只信任
  `AISUMMONER_CADDY_EDGE_IP` 这个精确地址。Caddy 覆盖而非追加
  `X-AISummoner-Client-IP`，不要把该信任项改成 subnet/CIDR。
- OpenCode 使用 `network_mode: service:server`，与 Server 共享 loopback。
- Server 私有 SQLite data volume 不挂给 OpenCode。
- 独立 workspace volume 以相同绝对路径挂给 Server/OpenCode；二者固定使用相同数值非 root UID/GID `10001:10001`，以读取严格的 `0700/0600` workspace。
- Server public health 只检查接收状态和一个有短 deadline 的 SQLite 查询，不声称模型健康。

容器收到终止信号后，Server 先停止新准入，再依次 join Agent/OpenCode、Terminal/SSH 和 Tunnel，停止 Bridge/public HTTP，最后关闭 SQLite。总关闭过程有统一时限。

## Remote Client systemd 示例

### Linux x86_64 AppImage

桌面交付物 `AISummoner-Remote-0.1.0-x86_64.AppImage` 同时包含 Qt GUI 和静态
Go daemon。必须由普通桌面用户运行；GUI 不读取 Device 私钥，也不会打开监听端口。
首次使用：

```bash
chmod +x AISummoner-Remote-0.1.0-x86_64.AppImage
./AISummoner-Remote-0.1.0-x86_64.AppImage
```

正式发行包已预置 AISummoner HTTPS 服务。首次打开会自动启动后台服务，无需填写
Server 地址；直接在“状态”页等待一次性配对码即可。“状态”页还会显示 Device ID、
配对码倒计时、当前连接阶段、
活跃控制会话总数和脱敏事件。刷新配对码会先明确提示它将关闭现有控制会话；暂停/
恢复均通过同 UID、mode `0600` 的本机 Unix socket 完成。关闭或重开 GUI 不会停止
daemon，数据固定保存在 `$HOME/.local/share/aisummoner`。

“设置 → 高级：自托管服务”只供自托管部署者覆盖服务 Origin，普通用户无需展开。
构建发行包时可用 `AISUMMONER_DEFAULT_SERVER_ORIGIN=https://example.com` 覆盖默认值；
它必须是无路径、无凭据、无 query/fragment 的 HTTPS Origin。

公共信任链无需额外设置。仅在受控测试 CA 场景下，可在启动 GUI 时设置
`SSL_CERT_FILE=/path/to/ca.crt`；它会由 GUI 原样继承给 daemon，不会写入设置文件。
不要使用 `--dev`、`--allow-root-dev` 或跳过证书验证。

AppImage 仍保留显式 CLI 兼容入口，参数不会经过 shell 求值：

```bash
./AISummoner-Remote-0.1.0-x86_64.AppImage --cli status
./AISummoner-Remote-0.1.0-x86_64.AppImage --cli pause
./AISummoner-Remote-0.1.0-x86_64.AppImage --cli resume
./AISummoner-Remote-0.1.0-x86_64.AppImage --cli refresh-pairing
```

仓库构建会在固定 Ubuntu 22.04/Qt 6.2 环境中编译并运行 QtTest，静态构建 Go
daemon，收集 AppDir 依赖，检查最大 glibc 版本、文件权限及未解析动态库，再用固定
SHA-256 的官方 AppImage 工具与 Type-2 runtime 封装：

```bash
make remote-ui-test
make remote-appimage

# 自托管发行包：在构建期写入自己的 HTTPS Origin
AISUMMONER_DEFAULT_SERVER_ORIGIN=https://control.example.com make remote-appimage

# 网络受限时也可显式传入已校验的固定工具：
./deploy/build-remote-client-appimage.sh dist/AISummoner-Remote-0.1.0-x86_64.AppImage \
  /path/to/appimagetool /path/to/runtime-x86_64
```

构建同时生成 `.sha256`。若目标 Ubuntu 没有可用 FUSE，可用
`APPIMAGE_EXTRACT_AND_RUN=1 ./AISummoner-Remote-0.1.0-x86_64.AppImage` 运行同一内容。
旧的 `make client-appimage` 仅保留为无 GUI CLI 回滚构建，不再是首选桌面交付物。

创建普通系统用户 `aisummoner`，安装 Client 到 `/usr/local/bin`，在 `/etc/aisummoner/client.env` 仅写入：

```text
AISUMMONER_SERVER_URL=https://aisummoner.example.com
```

然后复制 [deploy/aisummoner-client.service](deploy/aisummoner-client.service) 到
systemd unit 目录。Unit 使用 daemon 模式和
`/var/lib/aisummoner-client/client.sock`，长期 Device identity/data directory 为
mode `0700`，socket 为 mode `0600`，进程保持非 root。stdout 直接丢弃，stderr
只承载结构化非秘密日志；不再创建 `pairing-output.log`。

本机 status/pause/resume/refresh 操作必须以 `aisummoner` 服务用户运行，才能通过
同 UID 检查。例如：

```bash
sudo -u aisummoner /usr/local/bin/aisummoner-client status \
  --data-dir /var/lib/aisummoner-client
```

桌面 GUI 运行在桌面用户自己的 daemon 上，而不是借由权限提升访问另一用户的
system service。systemd 形态与桌面形态应使用不同 Unix 用户/数据目录，不应尝试
跨 UID 共享 socket。实际安装、启停和主机变更仍须在受控部署任务中执行。

## 常用验证命令

```bash
make test
make test-race
make web-install
make web-test
make web-build
make compose-config
make docker-build
```

为避免 OOM，Go 并发默认限制为 2，Node 构建限制为 2 GiB；不要并行运行 race、Node build 和 Docker build。
