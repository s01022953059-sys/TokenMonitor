# 任务

## 进行中

### TM-AM-002 让 `agentmemory context` 默认按项目根路由（WorkBuddy 上线前置）

- 状态：待处理
- 目标：在不传 `--root` 时，`agentmemory context` 应能根据当前工作目录自动发现最近的上级 `.agentmemory/manifest.yaml`，并以该项目为根构建任务知识包。
- 背景：2026-07-13 验证 `token_monitor` 接入时发现，**在 token_monitor 目录里跑 `context` 不传 `--root` 仍返回 AgentMemory 自己的结果**；必须显式传 `--root /Users/baggio/Projects/token_monitor` 才能命中本项目记忆包。WorkBuddy 上线后用户不会记得加 `--root`，必须自动发现。
- 验收：
  1. 在 token_monitor 目录下跑 `context "<query>"`（不传 `--root`），top-1 命中应为 `.agentmemory/current-state.md`（不再是 AgentMemory 主项目）
  2. 退回 AgentMemory 主项目目录时仍能正确返回主项目结果
  3. 进入无 `.agentmemory/` 的目录时应给出明确提示，**不**误命中主项目
  4. 显式 `--root` 仍可用、且优先级最高

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
