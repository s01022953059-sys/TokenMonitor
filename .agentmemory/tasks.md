# 任务

## 进行中

（无 —— TM-AM-002 已与 AM-005 合并实施完成，详见"已完成"。）

## 待处理

- TM-002：v1.4.33 修复（社区用量同步 + 测试报告隔离 + 年度热力图对齐）—— 与本任务无关，**不**自动执行
- TM-003：v1.4.32 → v1.4.33 → 发版流程——需要鹏帅明确说"发新版本"才能发布

## 已完成

- TM-000：v1.4.32 commit（`1da6bfb`）—— 改善社区用量同步提示
- TM-AM-001：token monitor 接入 AgentMemory（轻接入，建包 + push + AM-001 验收）
  - 提交：`5eb3fa3`（建包）+ `6018f74`（AGENTS.md append）
  - 推送：origin (GitCode) + github 双 remote ✅
  - 验证：`agentmemory context "token monitor 当前状态" --root ... --budget 3200` top-1 命中本项目 `.agentmemory/current-state.md:1`，score 12.0；8 条结果中 5 条来自新建 `.agentmemory/`，0 条误召回 AgentMemory 主项目
  - 时间：2026-07-13 17:14–17:24
  - 证据：`events.jsonl#memory.initialized` + `events.jsonl#integration.token_monitor_context_verified`
- TM-AM-002：让 `agentmemory context` 默认按项目根路由（与 AM-005 合并实施）
  - 时间：2026-07-13 17:30–17:34
  - 真实体验 3 场景：token_monitor 根 ✅ / token_monitor 子目录 ✅ / `/tmp` 无 `.agentmemory/` 明确报错 ✅
  - 证据：`AgentMemory/.agentmemory/events.jsonl#evt-20260713-0015` + `events.jsonl#evt-20260713-0014`
