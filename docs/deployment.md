# 自托管部署指南

本文描述当前 Alpha 的部署边界，不是生产 SLA。AISummoner 当前是单节点、单管理员
系统；上线前必须自行负责域名、TLS、备份、升级和公网防护。

## 1. 通用安全要求

- Server/Bridge/Runtime 不直接暴露公网；公网只进入受控 HTTPS reverse proxy。
- Remote Client 只主动出站，不开放远程控制监听端口。
- `.env`、SQLite、Runtime credentials 和 Device identity 使用私有目录与严格权限。
- `AISUMMONER_TRUSTED_PROXY_IPS` 只接受直接可信代理的精确数值 IP，不能写 hostname、
  CIDR 或整段子网。
- 代理必须覆盖 `X-AISummoner-Client-IP`，不能追加客户端自带值。
- 生产禁止 `--dev`、`--allow-root-dev`、明文 WS 与跳过证书验证。

完整边界见 [协议与安全基线](baseline/02-protocol-data-security.md)。

## 2. Docker Compose + Caddy

当前 Compose 示例支持 Fake、直接 DeepSeek 和 OpenCode；DSH 首轮部署仍使用同 UID
的直接 Server + 私有 Runtime 包，不在 Compose 中伪装成完整支持。

```bash
install -m 600 .env.example .env
# 编辑 hostname、管理员引导密码、两个独立 secret 和所选 Adapter 配置。
test "$(stat -c '%a' .env)" = 600

sh deploy/validate-compose.sh
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

模式约束：

- Fake：`AISUMMONER_AGENT_ADAPTER=fake`，`COMPOSE_PROFILES` 为空；
- DeepSeek：`AISUMMONER_AGENT_ADAPTER=deepseek`，配置 HTTPS API 与模型，profile 为空；
- OpenCode：`AISUMMONER_AGENT_ADAPTER=opencode`，同时设置
  `COMPOSE_PROFILES=opencode`。

`deploy/validate-compose.sh` 只调用 secret-safe 的
`docker compose config --quiet`，不会把插值后的环境输出到日志。

Compose 的设计约束：

- Caddy 是唯一公开 80/443 的服务；
- Server 数据卷不挂给 Runtime；
- OpenCode 与 Server 共享 network namespace，只用 loopback 通信；
- Server/OpenCode 固定相同非 root UID/GID 和独立 workspace volume；
- `/healthz` 只表示 Server/SQLite readiness，不声明模型可用。

## 3. DSH Runtime

DSH 是当前一等 Agent Runtime，但必须保持私有：Host 和 HMAC Capability Bridge 都只
绑定数值 loopback，Browser 不能直连 DSH。

运行包从固定 DSH checkout、Node/pnpm 版本和校验摘要构建：

```bash
mkdir -m 700 -p "$PWD/.package-work" dist
docker pull node:24.19.0-bookworm@sha256:934240a162082fd8b8a2f90cd5114446443f1eba1c5378f6687167ca405e6584

sh deploy/package-dsh-runtime-container.sh \
  /absolute/path/to/deepseek-harness \
  dist/aisummoner-dsh-runtime-linux-x64.tar.gz \
  "$PWD/.package-work"
```

解包后验证：

```bash
mkdir -m 700 /tmp/aisummoner-dsh-check
tar -xzf dist/aisummoner-dsh-runtime-linux-x64.tar.gz \
  -C /tmp/aisummoner-dsh-check
sh deploy/check-dsh-runtime.sh \
  /tmp/aisummoner-dsh-check/aisummoner-dsh-runtime
```

`node/` 与 `runtime/` 必须一起部署。DSH home 使用独立 `0700` 持久目录；Bridge secret
至少 32 字节，Host URL、Bridge listener 和 callback URL 必须精确对应 loopback。
配置字段见 [.env.example](../.env.example)。

管理员可在认证后的 Controller“设置 → Agent 与模型 → 模型供应商”配置 DSH 官方
供应商、内置兼容供应商或自定义 OpenAI/Anthropic 兼容网关。API Key 仅进入 Server
上 mode `0600` 的 DSH credential store，不返回 Browser，也不进入 AISummoner
SQLite、日志或审计。自定义 Base URL 只接受 HTTPS；只有与 Server 明确同机部署的
服务可使用数值 loopback HTTP。

Agent 输入框左下角显示当前 DSH 模型。选择供应商、模型或该模型声明的推理强度只
作用于当前 DSH Session 的下一步，并沿用同一个 AISummoner 对话；Provider Settings
和 Session 模型选择不是同一个全局开关。配置冲突会要求刷新，不会覆盖另一个页面
刚提交的修改。

## 4. Headless Remote systemd

服务器型 Remote 可以安装静态 `aisummoner-client` 并使用
[`deploy/aisummoner-client.service`](../deploy/aisummoner-client.service)。

`/etc/aisummoner/client.env` 至少设置：

```text
AISUMMONER_SERVER_URL=https://control.example.com
```

systemd 形态应使用专用非 root 用户与 `/var/lib/aisummoner-client`；Qt 桌面形态使用
当前桌面用户与 `$HOME/.local/share/aisummoner`。两者不能跨 UID 共享 Unix socket
或 Device identity。

本机控制命令必须以 daemon 所属用户执行：

```bash
sudo -u aisummoner /usr/local/bin/aisummoner-client status \
  --data-dir /var/lib/aisummoner-client
```

## 5. 升级与回滚

- 替换二进制前保留精确 SHA-256、SQLite/WAL/SHM 和配置快照；
- 停止新准入后，按 Agent → Terminal → Tunnel → HTTP → SQLite 顺序 joined shutdown；
- 不要用 broad `pkill`、删除共享 Docker network 或覆盖未知数据目录；
- Schema migration 只向前，回滚前先确认旧二进制能读取新 schema；
- 每次升级验证 login、Device reconnect、Terminal、Agent、日志脱敏和 rollback。

历史部署证据在 [`docs/acceptance/`](acceptance/)；它们是特定环境快照，不是可复制
粘贴的生产凭据或永久公共地址。
