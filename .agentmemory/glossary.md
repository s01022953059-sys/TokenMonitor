# 术语表

- **token monitor**：跨平台 AI/Agent 用量统计与可视化工具，本项目。
- **AgentsView** (`kenn-io/agentsview`)：统计口径参考仓库；统计字段含义不确定时优先查这里。
- **logs_2.sqlite**：Codex 本地历史日志库；与 rollout JSONL 合并后才能算全量 usage（v1.4.21 起）。
- **rollout JSONL**：Codex 每次会话/请求的逐次事件流，含 token 用量、模型、缓存命中。
- **单实例锁**：本地端口独占保护；以**内核独占锁为准**，不删残留锁文件。
- **年度热力图**：年度内每日用量的网格可视化，需和"本地总量"与"社区用量"对齐。
- **社区昵称**：用户公开昵称；与匿名 ID 分离，24h 3 次改名上限，30 天旧名保护。
- **v1.4.3x**：当前 v1.4.32（HEAD `1da6bfb`），正在准备 v1.4.33。
- **`agentmemory context`**：AgentMemory 的 CLI 子命令；按预算生成本任务的知识包。
- **轻接入**：只建 `.agentmemory/` 入口文件，**不**迁原文、不动现有业务文件。
