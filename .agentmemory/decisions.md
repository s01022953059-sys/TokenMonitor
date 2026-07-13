# 已确认决策

## 协作与发版

- 项目内交流称呼用户"鹏帅"。
- 每次更新代码后必须同步刷新 `README.md`（新功能、新限制、版本号、下载地址都要补）。
- **只有鹏帅明确说"发新版本"才能发布**：普通修复**不**改版本号、**不**打 tag、**不**推 Release。
- 版本号两处同步：`Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`。
- 发版流程：先 `bump` → `git commit+tag` → `bash release_all.sh`；`release_all.sh` 内部必须先调 `verify_release.sh`（Unit → API → E2E → 构建）才能进入 tag/Release 步骤。

## 统计与数据

- 统计口径不确定时优先参考 AgentsView (`kenn-io/agentsview`)，禁止仅凭字段名猜测。
- Codex 漏统已修：合并 `logs_2.sqlite` 与 rollout JSONL（v1.4.21）。
- WorkBuddy 改用逐请求 usage（替代历史聚合口径，v1.4.22 审计后落地）。
- 缓存语义：区分"请求内缓存"与"跨请求缓存"，避免重复计费。

## 社区与昵称

- 社区昵称与匿名 ID 分离；24h 内最多 3 次改名；30 天内旧名受保护。
- 测试报告与社区数据隔离（v1.4.32~v1.4.33 修复中）。

## 系统与锁

- 单实例锁以**内核独占锁为准**；不删除残留锁文件，避免 Windows 上误删活锁。
- `_singleton_check.py` 独立守护单实例。

## AgentMemory 接入

- 轻接入方案：只建 `.agentmemory/` 入口（manifest / brief / current-state / decisions / tasks / glossary / events），**不**迁 `.codex/project_memory.md` 原文，**不**迁 `docs/PROJECT_STATUS.md` 原文。
- 原 `AGENTS.md` 内容**不**改；只在文件末尾追加"AgentMemory 接入"小节。
- 工作区里 7 个 M 文件与本任务无关，**不**自动 add / commit。
- 接入本身**不**等于授权发版；要发版仍需鹏帅明确说"发新版本"。
