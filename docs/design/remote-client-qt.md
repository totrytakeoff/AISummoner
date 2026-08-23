# Remote Client Qt 6 GUI 设计规格

- 状态：Task016 输入草案
- 日期：2026-08-23
- Toolkit：Qt 6 Widgets + Qt Network，C++20，首阶段不用 QML/WebEngine
- 外部审稿：Claude Code 仅提供无写权限 UI/UX review；最终安全与产品边界由
  AISummoner ADR-0004 和 Task 计划决定

## 产品角色

这是被控 Linux 设备上的本机状态客户端，不是控制端。Qt GUI 只连接同 UID 的
Go daemon 私有 Unix socket。daemon 持有 Device Identity、Tunnel 和 Embedded
SSHD；GUI 不读取私钥、不执行命令、不开放 TCP listener。关闭 GUI 不停止 daemon。

## 信息架构

单窗口、三项左侧导航：

1. **状态**：Device ID、配对码、连接/被控状态、活跃控制会话、主要操作和最近事件。
2. **事件**：只读脱敏事件时间线及有限筛选。
3. **设置**：Server/外观/daemon 信息、未来本机权限和独立危险区。

第一版默认 940×640，最小 780×560。左侧 188px，可在较窄窗口折叠为 64px；
内容不足时滚动，不压缩配对码和主要操作。

## 状态页

```text
┌──────────────┬─────────────────────────────────────────────────────┐
│ AISummoner   │ 设备状态                              [● 在线]      │
│              │                                                     │
│ 状态         │ ┌ Device card ───────────────────────────────────┐ │
│ 事件         │ │ hostname                 Device ID       [复制]│ │
│ 设置         │ └────────────────────────────────────────────────┘ │
│              │                                                     │
│              │ ┌ Pairing card ──────────────────────────────────┐ │
│              │ │      A7KD-PQ52       08:42 后过期              │ │
│              │ │                     [复制] [刷新]               │ │
│              │ └────────────────────────────────────────────────┘ │
│              │                                                     │
│              │ ┌ 连接 ──────────┐ ┌ 当前控制会话 ─────────────┐ │
│              │ │ Online         │ │ 2                         │ │
│              │ └────────────────┘ └────────────────────────────┘ │
│              │                                                     │
│ ● Online     │                         [暂停连接]                 │
└──────────────┴─────────────────────────────────────────────────────┘
```

- 配对码只在 daemon 返回有效 offer 时显示，使用系统等宽字体，提供复制、到期进度和
  明确刷新。它不出现在事件列表、toast、错误或 debug 输出。
- 当前 Tunnel v1 只能可靠显示“活跃控制会话总数”，不能伪造 Terminal/Agent 分类；
  类型化统计留给独立协议升级。
- 暂停无二次确认但有即时反馈；恢复无确认；刷新配对码确认会关闭当前控制会话。
- 真正 Disconnect 与 Reset Identity 在协议/生命周期完成前不做假按钮。

## 状态矩阵

| daemon phase | 状态文案 | 主动作 | 配对区 | 会话 |
|---|---|---|---|---|
| starting/connecting | 正在连接 | 暂停 | loading/隐藏 | `—` |
| retrying | 正在重连 | 暂停 | 保留未过期 offer | 0 |
| online + offer | 等待配对 | 暂停 | code/countdown/copy/refresh | 0 |
| online | 在线 | 暂停 | 隐藏 | 实时总数 |
| paused | 已暂停 | 恢复 | 隐藏敏感码 | 0 |
| error | 连接错误 | 重试/暂停 | 视有效 offer | 0 |
| stopped/IPC unavailable | 后台服务不可用 | 启动服务 | 隐藏 | `—` |

所有状态都同时显示文字和图标，不能只靠颜色。

## 事件页

- 使用 `QListView + QAbstractListModel`，最多展示 daemon 的 200 条有序事件。
- 一条事件只含时间、固定类型、严重级别和固定摘要，例如“已连接到控制服务”、
  “一个控制会话已开始”。
- 禁止出现：配对码、Server URL、命令/cwd、Terminal/Agent 内容、SSH key、
  credential、raw transport error。
- 首版仅提供 All/Connection/Control 三个本地筛选，不做全文搜索敏感载荷。

## 设置页

- Server URL 与 Device name 属于非秘密设置；改变后必须通过 daemon 重启/重配合同，
  Task016 不得直接改正在运行 Core 的内存。
- 主题：跟随系统、浅色、深色。使用 `QStyleHints::colorScheme`。
- About：GUI/daemon 版本、IPC 状态、开源许可。
- 权限管理显示清晰的“后续版本”说明，不提供无效开关。
- Reset Identity 暂不启用；实现时必须输入 Device ID 后四位并经过 daemon 原子
  备份/重建流程，不能由 GUI 删除 key 文件。

## 视觉令牌

Apple-like 指清爽、克制、层级清晰，不复制 macOS 控件或隐藏 Linux 标题栏。

| token | Light | Dark |
|---|---|---|
| background | `#F5F6F8` | `#15171A` |
| surface | `#FFFFFF` | `#1F2227` |
| subtle | `#EEF1F5` | `#292D34` |
| primary text | `#1D1F23` | `#F1F2F4` |
| secondary text | `#626871` | `#A7ADB7` |
| accent | `#2477F3` | `#6B9CFF` |
| success | `#18864B` | `#44C783` |
| warning | `#B87508` | `#F0B34A` |
| danger | `#D83B3B` | `#F06B6B` |
| border | `#E1E5EA` | `#343943` |

- 间距基数 4：4/8/12/16/20/24/32；页面 24，卡片内 20。
- 卡片圆角 16，按钮 10，小标签 8，status pill 全圆角。
- 系统 UI 字体；正文 14px，辅助 12px，标题 22px，配对码 26px 等宽，计数 30px。
- 动画只用于 120–180ms 状态过渡；遵循 reduced-motion。

## Qt 组件边界

- `QMainWindow`、`QStackedWidget`、自定义 `NavigationRail`。
- `QLocalSocket` 的异步 `DaemonClient`，UI 线程不得执行阻塞 socket/进程等待。
- `StatusPage`、`EventsPage`、`SettingsPage` 和小型 reusable card/pill/button。
- 集中 `Theme`/QPalette/QSS，不在各 widget 内散落颜色。
- 每个按钮设置 accessible name/description；连接状态变化发布可访问性事件；
  完整键盘焦点顺序、可见 focus ring、Esc 关闭 dialog。

## Task016 最小验收

- 780×560 无重叠；浅/深/系统主题均可用且对比度合格。
- 同一真实 daemon 上显示 Device ID、有效配对码/倒计时、状态、控制会话和事件。
- Copy/refresh/pause/resume 具有 loading、防重复、失败恢复和正确确认边界。
- GUI 退出后 daemon/Tunnel 继续；重新打开恢复当前 snapshot/event cursor。
- daemon 不在时可从 AppImage 同目录安全启动；绝不使用 root 或开发 TLS 绕过。
- Qt GUI 日志、测试 artifact 和崩溃消息中无上述敏感载荷。
- 只依赖 Qt Core/Gui/Widgets/Network；不引入 QML/WebEngine。
