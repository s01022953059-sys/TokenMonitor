# token_monitor 交接索引

> 按 MAR 模板实例化（2026-09-07）。本文件是索引：代码状态以 Git 为准、测试以执行记录为准、项目历史状态见 `.agentmemory/current-state.md` 与 `docs/PROJECT_STATUS.md`，不在此复制。

## 当前快照

- 规则来源/commit/范围：`mar/`（Multi-Agent-Rule @ `4606982ea161962355411808e4c6d4277c1177e4`），适用于 token_monitor 全部开发/测试/发布任务，见 `AGENTS.md` MAR 采纳节
- 实际分支/完整 commit/工作区差异：`main` @ `7a874d06`（v1.5.22 + MAR 采纳 + memory 同步两个文档 commit），**工作区干净**；origin/GitCode 与 github 双远端 2026-09-07 已同步（github 原落后 120 commit 已补齐）
- 运行环境与实际部署：macOS（Swift 壳 + Python 后端）/ Windows（Go 单体），发布走 `release_all.sh` → GitCode Releases；最新已发布版本 v1.5.22
- 集成负责人/当前写入者：鹏帅（唯一授权发布人）；单一写入者模式，无并发 Agent

## 活动任务

| task_id | owner | 目标与验收条件 | 基线/修改范围 | 依赖 | 状态 | 证据索引 |
| --- | --- | --- | --- | --- | --- | --- |
| TASK-MAR-01 | Claude (本会话) | 在 token_monitor 启用 MAR：固定副本 + AGENTS.md 采纳记录 + HANDOFF 实例化 | 2418878b / `mar/`、`AGENTS.md`、`HANDOFF.md`、`.agentmemory/events.jsonl` | 无 | passed（文件级） | 7 文件 SHA-256 对齐 4606982（本会话终端输出） |
| TASK-MAR-02 | Claude (本会话) | 遗留任务全面盘点：重建 `.agentmemory/tasks.md` 台账（P0 token / github 落后 / 工作区处置 / 记忆滞后 / v1.4.42 候选复核 / 暂缓项复捡条件） | 2418878b / `.agentmemory/tasks.md`、`HANDOFF.md` | 无 | passed（整理完成） | tasks.md 2026-09-07 版；grep/rev-list 实测见本会话 |
| TASK-P1-01 | Claude (本会话) | 处理 P1：github push（120+2 commit）/ 二进制恢复 HEAD（备份 /tmp）/ zcode plan 补提交 / memory 同步至 v1.5.22（current-state + brief 重写） | 2418878b→7a874d06 / `.agentmemory/*`、远端 main | 鹏帅指令"请处理 P1 的内容" | passed | commit `dd1636e8`+`7a874d06`；push 复核双远端 0/0；evt-20260907-0002 |

历史任务与决策：`.agentmemory/tasks.md`（2026-09-07 已重建为唯一任务台账）、`.agentmemory/decisions.md`。

## 验证与授权

- 候选源码 commit 或快照 SHA-256：`7a874d06`（工作区干净，HEAD 即测试对象基线；业务代码与 v1.5.22 发布版一致，本次仅文档/memory 变更）
- 产物 SHA-256：v1.5.22 发布产物见 GitCode Release 与 `release_all.sh` 校验输出（本次未产新产物）
- 测试命令/环境/时间/退出码/报告：本次 MAR 启用 + P1 处理**未运行**项目测试（纯文档/memory/仓库卫生，零业务代码变更）；项目冒烟测试要求见 memory `smoke-tests.md`，发版前必跑
- 技术状态：not_run（业务代码未变更，无需重评）
- 用户验收：confirmed（部分）— 鹏帅 2026-09-07 会话指令："请在现在这个项目启用"、"请先帮我整理一下遗留任务"、"明文 token 请忽略"（D-2026-09-07-01）、"请处理 P1 的内容"；范围 = MAR 启用 + 任务盘点 + P1 三项
- 发布状态：not_run（无版本变更/tag/Release；发版仍需鹏帅明确说"发新版本"）
- 外部动作授权：git push github+origin main 已执行完毕（鹏帅 P1 指令覆盖 TM-2026-07-18-03，2026-09-07，双远端复核 0/0）；无其他在途外部授权

## 中断与恢复

- 已完成/失败/尚未执行：已完成 MAR 启用（TASK-MAR-01）、任务盘点（TASK-MAR-02）、P1 三项（TASK-P1-01：github push / 工作区处置 / memory 同步至 v1.5.22）；无失败项；P2 候选与暂缓项待鹏帅发起
- 在途操作及结果未知项：无
- 任务释放/接管人与时间：未发生
- 下一步、阻塞、恢复验证：① P2 候选攒批（更新文案双端对齐；CDN retry 已降级已知限制），发版需鹏帅明确说"发新版本"；② 下次发版按 GOV-05/09 在本文件追加验证与发布记录；③ 明文 token 已按 D-2026-09-07-01 关闭，不再复提
- 关联 PRD/TESTCASES/UI 证据及历史档案：PRD/TESTCASES 模板未实例化（按需再建）；UI 变更验证沿用现有 `tests/e2e_ui.sh` + 冒烟测试，UI 证据要求见 `mar/UNIVERSAL_UI_DESIGN_SYSTEM.md`
