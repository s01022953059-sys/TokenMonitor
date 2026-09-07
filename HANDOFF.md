# token_monitor 交接索引

> 按 MAR 模板实例化（2026-09-07）。本文件是索引：代码状态以 Git 为准、测试以执行记录为准、项目历史状态见 `.agentmemory/current-state.md` 与 `docs/PROJECT_STATUS.md`，不在此复制。

## 当前快照

- 规则来源/commit/范围：`mar/`（Multi-Agent-Rule @ `4606982ea161962355411808e4c6d4277c1177e4`），适用于 token_monitor 全部开发/测试/发布任务，见 `AGENTS.md` MAR 采纳节
- 实际分支/完整 commit/工作区差异：`main` @ `2418878bdafe7862da9b43b8482f4e92cb102317`（v1.5.22 已发布）；工作区**不干净** — 4 个 M（`.agentmemory/decisions.md`、`.agentmemory/events.jsonl`、`community_relay/token-monitor-community-relay`、`go_build/token-monitor`）+ 本次新增 `mar/`、`HANDOFF.md`、AGENTS.md 修改，均未提交
- 运行环境与实际部署：macOS（Swift 壳 + Python 后端）/ Windows（Go 单体），发布走 `release_all.sh` → GitCode Releases；最新已发布版本 v1.5.22
- 集成负责人/当前写入者：鹏帅（唯一授权发布人）；单一写入者模式，无并发 Agent

## 活动任务

| task_id | owner | 目标与验收条件 | 基线/修改范围 | 依赖 | 状态 | 证据索引 |
| --- | --- | --- | --- | --- | --- | --- |
| TASK-MAR-01 | Claude (本会话) | 在 token_monitor 启用 MAR：固定副本 + AGENTS.md 采纳记录 + HANDOFF 实例化 | 2418878b / `mar/`、`AGENTS.md`、`HANDOFF.md`、`.agentmemory/events.jsonl` | 无 | passed（文件级） | 7 文件 SHA-256 对齐 4606982（本会话终端输出） |
| TASK-MAR-02 | Claude (本会话) | 遗留任务全面盘点：重建 `.agentmemory/tasks.md` 台账（P0 token 泄露 / github 落后 120 commit / 工作区处置 / 记忆滞后 / v1.4.42 候选复核 / 暂缓项复捡条件） | 2418878b / `.agentmemory/tasks.md`、`HANDOFF.md` | 无 | passed（整理完成；各项执行待鹏帅授权） | tasks.md 2026-09-07 版；grep/rev-list 实测见本会话 |

历史任务与决策：`.agentmemory/tasks.md`（2026-09-07 已重建为唯一任务台账）、`.agentmemory/decisions.md`。

## 验证与授权

- 候选源码 commit 或快照 SHA-256：`2418878bdafe7862da9b43b8482f4e92cb102317` + 上述未提交差异（脏工作区，测试对象若涉及工作区代码须另记快照摘要）
- 产物 SHA-256：v1.5.22 发布产物见 GitCode Release 与 `release_all.sh` 校验输出（本次未产新产物）
- 测试命令/环境/时间/退出码/报告：本次 MAR 启用**未运行**项目测试（纯文档/规则文件变更）；项目冒烟测试要求见 memory `smoke-tests.md`，发版前必跑
- 技术状态：not_run（项目代码未变更）
- 用户验收：pending（鹏帅指令"请在现在这个项目启用"= 授权执行；验收待确认本节内容）
- 发布状态：not_run（MAR 启用不触发任何版本变更/tag/Release/push）
- 外部动作授权：仅本地文件写入。发布仍需鹏帅明确说"发新版本"；不自动 install 到 ~/Applications

## 中断与恢复

- 已完成/失败/尚未执行：已完成 `mar/` 固定副本（7 文件哈希校验通过）、AGENTS.md 采纳节、本文件、events.jsonl 入账；尚未执行 git commit（等鹏帅决定是否连同工作区其他 M 文件一起提交）
- 在途操作及结果未知项：无
- 任务释放/接管人与时间：未发生
- 下一步、阻塞、恢复验证：① 鹏帅决定是否处理 AGENTS.md 第 9 行明文 GitCode token（MAR SECURITY 遗留风险）；② 下次发版时按 GOV-05/09 在本文件追加验证与发布记录
- 关联 PRD/TESTCASES/UI 证据及历史档案：PRD/TESTCASES 模板未实例化（按需再建）；UI 变更验证沿用现有 `tests/e2e_ui.sh` + 冒烟测试，UI 证据要求见 `mar/UNIVERSAL_UI_DESIGN_SYSTEM.md`
