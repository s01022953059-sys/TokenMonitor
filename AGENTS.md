# AGENTS.md

## 约定

1. 每次和用户交流时, 称呼用户为"鹏帅"
2. **每次更新代码后必须同步刷新 README.md** — 新功能、新限制、版本号、下载地址
3. 版本号两处同步: `Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`
4. 发布流程: bump 版本 → git commit + tag → `bash release_all.sh`
5. GitCode token: ydMwBZbLaiex8hRqi-2cma3k
6. GitCode 不支持删除 release 附件, 每次发版用新 tag

## 项目记忆

详见 `.codex/project_memory.md`

## 架构概要

- macOS: Swift 壳 + Python 后端 + HTML 前端
- Windows: Go 单体 (go_build/main.go), 系统托盘, 交叉编译
- 两版功能完全对齐, 前端同一份 index.html + chart.js
- 详细文档: `docs/PROJECT_STATUS.md`

## AgentMemory 接入（轻接入，2026-07-13）

- 项目已轻接入 AgentMemory：新建 `.agentmemory/` 目录作为跨 Agent 入口，**不**迁 `.codex/project_memory.md` 原文、**不**迁 `docs/PROJECT_STATUS.md` 原文。
- 接手顺序：`.agentmemory/manifest.yaml` → `brief.md` + `current-state.md` → 按需查 `decisions.md` / `tasks.md` / `glossary.md` / `events.jsonl`。
- 发版硬约束**未**变：要发布仍需鹏帅明确说"发新版本"；本接入不授权任何版本号变更、tag、Release 或 push。
- 工作区里 7 个 M 文件（`go_build/static/index.html`、`index.html`、`scanner.py`、`server.py`、`tests/api_contract.py`、`tests/e2e_ui.sh`、`tests/run_unit_tests.sh`）与本接入无关，**不**自动 add / commit。
- 入账事件：`.agentmemory/events.jsonl` 第 1 条 `memory.initialized`。
