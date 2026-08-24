# 快速开始

AISummoner 当前是 source-first Alpha，没有正式 GitHub Release。下面分别说明本地
开发、Linux Remote AppImage 和最小产品流程。

## 环境要求

- Go 1.23+
- Node.js 20.19+ 与 npm
- Linux（Remote Client 当前只验证 Linux x86_64）
- 构建 Qt AppImage 时需要 Docker

建议至少保留 4 GiB 可用内存。Go race、Node build 和 Docker build 不要并行。

## 本地 Fake Agent 开发

### 1. 准备配置

```bash
git clone https://github.com/totrytakeoff/AISummoner.git
cd AISummoner
install -m 600 .env.example .env
```

编辑 `.env`：

- `AISUMMONER_ADMIN_PASSWORD`：首次管理员引导密码；
- `AISUMMONER_SESSION_SECRET`：至少 32 字节随机值；
- `AISUMMONER_PAIRING_SECRET`：另一份独立的至少 32 字节随机值；
- `AISUMMONER_AGENT_ADAPTER=fake`；
- Web/Vite 双进程开发时，把 `AISUMMONER_BASE_URL` 改为
  `http://127.0.0.1:5173`，保持 Server 监听
  `AISUMMONER_LISTEN_ADDR=127.0.0.1:8080`。

可以用 `openssl rand -base64 48` 分别生成两个 secret。不要把 `.env` 提交到 Git。

### 2. 启动 Server

```bash
set -a
. ./.env
set +a
GOMAXPROCS=2 go run ./cmd/aisummoner-server
```

首次管理员创建成功后，应从运行环境中删除
`AISUMMONER_ADMIN_PASSWORD`，再重启 Server。

### 3. 启动 Web 开发服务器

另开一个终端：

```bash
npm --prefix web ci
AISUMMONER_WEB_API_ORIGIN=http://127.0.0.1:8080 \
  npm --prefix web run dev -- --host 127.0.0.1
```

打开 `http://127.0.0.1:5173`。Vite 只代理 `/api` 与 WebSocket；Server 仍是认证、
Origin 和业务权威。

## 启动开发 Remote Client

旧的 Go CLI 适合开发链路验证：

```bash
GOMAXPROCS=2 go run ./cmd/aisummoner-client start \
  --server http://127.0.0.1:8080 \
  --dev
```

必须使用普通用户。只有明确的本地开发才允许 `--dev`；生产禁止明文与跳过 TLS。
这个兼容入口把配对码写到 stdout，因此不要把 stdout 送入公共日志。

## 构建 Linux Remote AppImage

正式桌面产物是：

```text
AISummoner-Remote-0.1.0-x86_64.AppImage
```

它包含 Qt GUI 和 Go daemon。旧的 `AISummoner-Client-*.AppImage` 是 CLI-only
回滚产物，没有 GUI，也没有零配置启动逻辑；不要把两者混用。

```bash
AISUMMONER_DEFAULT_SERVER_ORIGIN=https://control.example.com \
  make remote-appimage

sha256sum -c dist/AISummoner-Remote-0.1.0-x86_64.AppImage.sha256
chmod +x dist/AISummoner-Remote-0.1.0-x86_64.AppImage
dist/AISummoner-Remote-0.1.0-x86_64.AppImage
```

如果系统没有 FUSE：

```bash
APPIMAGE_EXTRACT_AND_RUN=1 \
  dist/AISummoner-Remote-0.1.0-x86_64.AppImage
```

GUI 首次启动会自动启动同包 daemon。数据位于
`$HOME/.local/share/aisummoner`；关闭 GUI 不会断开后台 Tunnel。高级设置可以覆盖
构建期 Server Origin，但普通发行用户不应被要求填写地址。

AppImage 的本地控制入口：

```bash
dist/AISummoner-Remote-0.1.0-x86_64.AppImage --cli status
dist/AISummoner-Remote-0.1.0-x86_64.AppImage --cli pause
dist/AISummoner-Remote-0.1.0-x86_64.AppImage --cli resume
dist/AISummoner-Remote-0.1.0-x86_64.AppImage --cli refresh-pairing
```

## 完成一次产品流程

1. 在普通 Linux 用户下启动 Remote GUI。
2. 等待状态页出现未过期的一次性配对码。
3. 登录 Browser Controller，在 Device Hub 输入配对码。
4. 打开 Device Workspace，先用 Terminal 执行无害命令。
5. 新建 Agent Session；Fake 模式会走确定性审批/执行链。
6. 测试完成后可在 Remote GUI 暂停连接，或在 Controller 解除绑定。

真实 DSH、OpenCode 或 DeepSeek 需要额外 Runtime/credential 配置，参见
[部署指南](deployment.md)。
