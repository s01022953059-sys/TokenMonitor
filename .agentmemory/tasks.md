# 任务

## 进行中

### TM-AM-001 token monitor 接入 AgentMemory（轻接入）

- 状态：进行中（建包完成，待鹏帅试跑 `agentmemory context` 验证）
- 目标：让 Codex / WorkBuddy / Claude Code 能从 token_monitor 的项目记忆包接手任务，按来源精确定位决策。
- 当前步骤：建入口文件；在 `AGENTS.md` 末尾追加接入小节。
- 验收：从全新 Agent 视角读取 `manifest.yaml` → `brief.md` → `current-state.md` 后，能在两分钟内恢复项目身份、当前版本、进行中工作、硬约束与下一步。

## 待处理

- TM-002：v1.4.33 修复（社区用量同步 + 测试报告隔离 + 年度热力图对齐）—— 与本任务无关，**不**自动执行
- TM-003：v1.4.32 → v1.4.33 → 发版流程——需要鹏帅明确说"发新版本"才能发布

## 已完成

- TM-000：v1.4.32 commit（`1da6bfb`）—— 改善社区用量同步提示
- TM-AM-000：建 `.agentmemory/` 入口包（本文件为证据）
