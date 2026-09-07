# AGENTS.md

## 约定

1. 每次和用户交流时, 称呼用户为"鹏帅"
2. **每次更新代码后必须同步刷新 README.md** — 新功能、新限制、版本号、下载地址
3. 版本号两处同步: `Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`
4. 发布流程: bump 版本 → git commit + tag → `bash release_all.sh`
5. GitCode token: ydMwBZbLaiex8hRqi-2cma3k
6. GitCode 不支持删除 release 附件, 每次发版用新 tag
7. **每次发现 bug 或根据反馈改动代码, 必须在同一次提交中补充或扩充测试用例** (Python 放在 `tests/`,Go 放在 `go_build/*_test.go`),覆盖该修复的场景、输入和预期输出。

## 项目记忆

详见 `.codex/project_memory.md`

## 架构概要

- macOS: Swift 壳 + Python 后端 + HTML 前端
- Windows: Go 单体 (go_build/main.go), 系统托盘, 交叉编译
- 两版功能完全对齐, 前端同一份 index.html + chart.js
- 详细文档: `docs/PROJECT_STATUS.md`

## AgentMemory 已移除（2026-09-07）

- `.agentmemory/` 目录已按鹏帅指示**删除**，信息全部迁入 MAR 文档体系（零丢失），迁移映射与恢复方式见 `docs/HISTORY.md`。
- 原"接手顺序"改为：`HANDOFF.md` → 本文件 → 按需查 `PRD.md` / `TESTCASES.md` / `docs/DECISIONS.md` / `docs/HISTORY.md`。
- 发版硬约束**未**变：要发布仍需鹏帅明确说"发新版本"。
- 迁移入账：`docs/events.jsonl#evt-20260907-0004`。

## Multi-Agent-Rule (MAR) 采纳（2026-09-07）

- **规则来源**：本地规则包 `~/Projects/Multi-Agent-Rule`，固定 commit `4606982ea161962355411808e4c6d4277c1177e4`（采纳时该仓库工作区干净，HEAD 即此 commit）。
- **项目内固定副本**：`mar/` 目录（母本 GOV 规范 + UI 规范 + docs/ + rules/，7 个文件已逐一 SHA-256 对齐锁定 commit）。会话内引用规则以 `mar/` 副本为准；升级时先审阅 MAR 仓库 diff、重新固定 commit 并更新本节，不追随远端最新。
- **适用范围**：token_monitor 全部开发、测试、发布任务。规则母本 GOV-01 至 GOV-12 + UI 规范 UI-01 至 UI-08（本项目有 UI：`index.html` / `community_dashboard.html`，GOV-10 适用）。
- **与既有规则的关系**：MAR 不覆盖本文件既有约定与用户明确指令；两者冲突时以鹏帅当前明确指令为准。既有发版硬约束（鹏帅明确说"发新版本"才 release、不自动 install 到 ~/Applications、发版前跑完整冒烟测试、bug 修复攒批发版）即 GOV-06/GOV-09 要求的项目级授权策略，继续有效。
- **交接索引**：`HANDOFF.md`（MAR 模板实例）= 交接快照 + 活动任务唯一台账。
- **文档地图（GOV-04 分工）**：业务规格与验收 → `PRD.md`（REQ-01~08 + 附录 A 数据源口径 / 附录 B 术语表）；用例索引与缺陷 → `TESTCASES.md`（TC-A~E、BUG-01/02）；决策记录 → `docs/DECISIONS.md`（D-xxxx 稳定 ID）；事件审计 → `docs/events.jsonl`；版本史与已完成归档 → `docs/HISTORY.md`；项目状态全量 → `docs/PROJECT_STATUS.md`。
- **已知例外**：无新增例外。`AGENTS.md` 明文 GitCode token 一项，鹏帅 2026-09-07 明确指示忽略（已接受风险，见 `docs/DECISIONS.md#D-2026-09-07-01`），不再复提。
- 入账事件：`docs/events.jsonl#evt-20260907-0001`（rules.mar_adopted）。
