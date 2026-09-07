# 任务归档

> 2026-09-07 按 MAR GOV-04 重构：**活动任务唯一台账在 `HANDOFF.md`**，本文件只保留已完成/已关闭历史，不再维护"进行中/待处理"（避免双台账漂移）。任务状态口径：not_run / passed / failed / blocked（GOV-05）。

## 已关闭（不执行）

- TM-2026-09-07-01：`AGENTS.md` 明文 GitCode token 迁移+轮换 — 鹏帅 2026-09-07 明确指示忽略，记 `decisions.md#D-2026-09-07-01`（已接受风险），不再复提。

## 已完成（2026-09-07，MAR 启用 + P1 处理）

- TASK-MAR-01：MAR 启用 — `mar/` 固定副本（7 文件 SHA-256 对齐 `4606982`）、`AGENTS.md` 采纳节、`HANDOFF.md` 实例化。commit `dd1636e8`。
- TASK-MAR-02：遗留任务全面盘点 — 重建台账、v1.4.42 候选复核（grep 实测双端文案仍不一致）、github 落后实测扩大到 120 commit。
- TASK-P1-01：P1 三项 — ① github push（120+2 commit，双远端复核 0/0）；② 工作区处置：9/6 冒烟临时构建二进制恢复 HEAD（备份 `/tmp/tm-backup-*-20260907`）、`.zcode` 旧 plan 按惯例补提交；③ 记忆同步：`current-state.md` 重写至 v1.5.22（57 版主题摘要）、`brief.md` 补 MiniMax Code。commit `7a874d06`+`b78cf9c7`，证据 `events.jsonl#evt-20260907-0002`。
- TASK-MAR-03：按 MAR 四文档模型重构记录 — 新建 `PRD.md`（REQ-01~08）+ `TESTCASES.md`（TC-A~E、BUG-01/02），`HANDOFF.md` 改为唯一活动台账，本文件改为归档。

## 已完成（历史）

- TM-2026-07-18-01：memory 同步 v1.4.32→v1.4.41 — 证据 `events.jsonl#evt-20260718-0001`（2026-09-07 盘点时补记完成）
- TM-000：v1.4.32 commit（`1da6bfb`）— 改善社区用量同步提示
- TM-AM-001：token_monitor 接入 AgentMemory（轻接入，建包 + push + AM-001 验收）
  - 提交 `5eb3fa3`+`6018f74`；origin+github 双 remote；`agentmemory context` top-1 命中验证
  - 证据：`events.jsonl#memory.initialized` + `#integration.token_monitor_context_verified`
- TM-AM-002：`agentmemory context` 按项目根路由（与 AM-005 合并实施）
  - 3 场景实测（根/子目录/无 .agentmemory 报错）；证据 `AgentMemory/.agentmemory/events.jsonl#evt-20260713-0015` + 本地 `#evt-20260713-0014`
- TM-2026-07-18-10~19：v1.4.33~v1.4.41 发布系列（每日详情加速、fork 崩溃修复、缓存复用、组合筛选等，9 版）+ 跨平台性能抖动阈值放宽（`fd17b1c`）
- v1.4.42~v1.5.22 发版历史：见 `current-state.md` 版本史摘要（57 版主题分组），逐版 commit 以 `git log` 为准
