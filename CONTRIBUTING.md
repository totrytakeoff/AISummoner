# Contributing to AISummoner

AISummoner 正处于快速演进的 Alpha。欢迎 Issue、文档修正、测试和小范围 PR；涉及协议、
信任边界、Runtime 或跨平台生命周期的变更，请先讨论设计。

## 开始之前

1. 阅读 [README](README.md) 与 [文档导航](docs/README.md)。
2. 阅读根目录 [AGENTS.md](AGENTS.md) 中的范围、安全和验证约束。
3. 涉及架构时，先检查 [baseline](docs/baseline/) 与 [ADR](docs/decisions/)。
4. 不要在一个 PR 中同时展开多个 Roadmap 阶段。

## 开发环境

- Go 1.23+
- Node.js 20.19+ / npm
- Qt 6.2+、CMake 3.22+（Remote GUI）
- Docker（生产 Web、AppImage、Compose/Runtime 构建）

本地启动见 [快速开始](docs/quick-start.md)。

## 验证

按改动范围先跑 focused tests；准备合入时运行：

```bash
GOMAXPROCS=2 go test -p 2 ./...
GOMAXPROCS=2 go test -p 2 -race ./internal/...
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run
NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build
```

Remote GUI：

```bash
make remote-ui-test
```

涉及部署时再验证：

```bash
sh deploy/validate-compose.sh
```

不要并行运行 Go race、Node build、Qt/Docker build。资源不足或外部 Provider 不可用时，
如实记录未验证项，不要把跳过写成通过。

## 安全与数据

- 不提交 `.env`、数据库、日志、API Key、Cookie、SSH/Device key 或真实配对码。
- 测试使用显式 synthetic sentinel，不能复用生产 credential。
- Provider/Runtime 不能选择 Device 或访问 Server 本地执行面。
- 新协议输入必须有长度、数量、时间和并发上限。
- 保留 owner predicate、Origin、TLS、SSH Host Key、审批和 joined shutdown 回归。

安全漏洞请按 [SECURITY.md](SECURITY.md) 私下报告。

## 提交与文档

- 使用聚焦、可回滚的提交；建议 Conventional Commit 风格。
- 行为、配置、协议或 schema 改动必须同步相应测试和文档。
- 核心决策改变先更新/新增 ADR，再实现。
- 数据库 migration 只新增，不重写已发布 migration。
- 第三方实现或视觉移植必须保留许可证与
  [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md)。

AI 编码 Agent 可以使用 `docs/agent_context/` 做耐久交接，但最终 PR 必须让人类读者
仅凭代码、测试和公开文档即可理解，不依赖聊天历史。

## License

项目级许可证尚未确定。在许可证落地前，请不要假设公开仓库自动允许再分发；可以先
通过 Issue/PR 贡献，第三方代码继续遵循其原许可证。
