# token monitor 项目速览

跨平台的 AI/Agent 用量统计与可视化工具。统计 Codex、Claude Code、WorkBuddy 等多个数据源的 token 用量，本地落库后通过 Web 控制台展示，并提供社区昵称、年度热力图、Windows 安装程序等扩展能力。

## 产品体验

- macOS 端：Swift 壳 (`app_wrapper.swift`) 启动 Python 后端 (`server.py` + `scanner.py`)；前端共用 `index.html` + `chart.js`。
- Windows 端：Go 单体（`go_build/main.go`），独立安装程序。
- 单实例锁保护本地端口；社区昵称与匿名 ID 分离，24h 3 次改名上限，30 天旧名保护。
- 统计口径不确定时优先参考 AgentsView (`kenn-io/agentsview`)，不靠字段名猜测。

## 数据源

- Codex：`logs_2.sqlite` + rollout JSONL（漏统已修，见 v1.4.21）
- Claude Code：原生日志路径
- WorkBuddy：逐请求 usage（替代历史聚合口径）
- 缓存语义：区分"请求内缓存"与"跨请求缓存"，避免重复计费

## 当前阶段

v1.4.32 已 commit 到 `main`（HEAD `1da6bfb`）。正在进行 v1.4.33 的社区用量同步 + 测试报告隔离 + 年度热力图对齐修复；本机工作区有 7 个未提交的 M 文件（与本任务无关，**不**会被自动提交）。

详细接手见 `AGENTS.md`、`.codex/project_memory.md` 和 `docs/PROJECT_STATUS.md`。
