# token_monitor 交接索引

> MAR GOV-04 事实来源分工：**本文件 = 交接 + 活动任务唯一台账**；业务规格 → `PRD.md`；用例与缺陷 → `TESTCASES.md`；项目指引 → `AGENTS.md`；决策 → `docs/DECISIONS.md`；事件审计 → `docs/events.jsonl`；版本史与已完成归档 → `docs/HISTORY.md`。代码以 Git、测试以 `tests/reports/`、部署以 GitCode Release 为准，本文件不复制可变状态。

## 当前快照（2026-09-07 核实）

- 规则：MAR @ `4606982`（固定副本 `mar/`，采纳记录 `AGENTS.md`）
- 代码：`main` @ `b78cf9c7`，工作区干净；origin/GitCode 与 github 双远端同步（github 原落后 120 commit，2026-09-07 补齐）
- 发布：**v1.5.22** 已发布（2026-09-06，tag 已推 origin）；其后 3 个 commit 均为文档/memory，零业务代码变更
- 写入者：单一写入者模式（鹏帅 + 其授权的会话 Agent），无并发
- 验证有效性：业务代码测试以 v1.5.22 报告为准（7 项全过，`tests/reports/v1.5.22-pre-release-*.json`）；GOV-05 复用依据 = 此后无业务代码改动

## 活动任务（唯一台账）

| task_id | 优先级 | owner | 目标与验收条件 | 状态 | 备注/复捡条件 |
| --- | --- | --- | --- | --- | --- |
| TM-2026-07-18-02a | P2 | 待指派 | 更新进度文案双端对齐：macOS `app_wrapper.swift`"下载更新包 (…)" vs Windows `update_windows.go`"下载中 x%"（2026-09-07 grep 复核仍未做）。验收 = REQ-03 双端一致 | not_run | 攒批，发版需鹏帅明确说"发新版本" |
| TM-2026-07-18-02b | P3 | — | CDN 占位 retry 优化 | blocked（外部） | 即 `TESTCASES.md#BUG-01`，根因在 GitCode CDN 侧，已降级已知限制 |
| TM-2026-07-18-04 | P3 | 待指派 | AgentMemory 阶段 3：`.codex/project_memory.md`"事实"蒸馏进 `decisions.md` | not_run | 需鹏帅发起，不自动执行 |
| TM-2026-07-30-01 | 暂缓 | — | TRAE IDE 接入 | blocked（暂缓） | 复捡条件 ①②③ 见 `docs/DECISIONS.md#D-2026-07-30-01` |
| TM-2026-08-27-01 | 暂缓 | — | 豆包工作接入 | blocked（暂缓） | 复捡条件 ①②③ 见 `docs/DECISIONS.md#D-2026-08-27-01` |
| TM-LEGACY-01 | 长期 | 鹏帅 | SMAppService daemon 注册 | blocked（外部） | 需 Apple Developer Account |

已关闭：TM-2026-09-07-01（明文 token，鹏帅决定忽略，`docs/DECISIONS.md#D-2026-09-07-01`，不再复提）。
本日完成：TASK-MAR-01（MAR 启用）、TASK-MAR-02（任务盘点）、TASK-P1-01（github push / 工作区处置 / memory 同步至 v1.5.22）、TASK-MAR-03（四文档重构）、TASK-MAR-04（**AgentMemory 迁移删除**：`.agentmemory/` 全部信息迁入 MAR 文档体系，映射表见 `docs/HISTORY.md`，删除前已提交 `3571278c` 可恢复），证据 `docs/events.jsonl#evt-20260907-0001~0004`，归档 `docs/HISTORY.md`。

## 验证与授权

- 技术状态：v1.5.22 报告 passed（复用依据见快照）；当前文档 commit 无需测试（not_required，依据 GOV-05 低风险文档条款）
- 用户验收：confirmed（2026-09-07 会话指令链：启用 MAR → 整理遗留任务 → token 忽略 → 处理 P1 → 按 MAR 刷新文档结构）
- 发布状态：not_run；发布授权策略 = REQ-04（仅鹏帅明确说"发新版本"）
- 外部动作：本日已执行 git push origin+github（P1 指令覆盖，已复核 0/0）；无在途外部授权
- 二进制处置记录：9/6 冒烟临时构建的 `community_relay`/`go_build` 二进制已恢复 HEAD（惯例：v1.5.03 起不入库），备份 `/tmp/tm-backup-*-20260907`

## 中断与恢复

- 在途操作：无；失败项：无
- 下一步：① P2 文案对齐择机攒批；② 下次发版按 GOV-05/09 在本文件追记验证+发布记录并重跑冒烟（`tests/smoke.sh` 全过）
- 接手顺序：本文件 → `AGENTS.md` → 按需查 `PRD.md` / `TESTCASES.md` / `docs/DECISIONS.md` / `docs/HISTORY.md`
