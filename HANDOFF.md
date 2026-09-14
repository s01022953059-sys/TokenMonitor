# token_monitor 交接索引

> MAR GOV-04 事实来源分工：**本文件 = 交接 + 活动任务唯一台账**；业务规格 → `PRD.md`；用例与缺陷 → `TESTCASES.md`；项目指引 → `AGENTS.md`；决策 → `docs/DECISIONS.md`；事件审计 → `docs/events.jsonl`；版本史与已完成归档 → `docs/HISTORY.md`。代码以 Git、测试以 `tests/reports/`、部署以 GitCode Release 为准，本文件不复制可变状态。

## 当前快照（2026-09-07 核实）

### 2026-09-12 接手：TASK-CODEX-PROJECTS-01

- 授权：本会话用户要求开发 Codex 项目消耗分析，并参考 codex-reset.com。
- 基线：c87a6182ba788dd3a1503ddc0e40e51b670c6d4d；原有 AGENTS.md 修改和未跟踪 CLAUDE.md 不属于本任务，保持不动。
- owner：Codex 主代理集成，Luna 分别负责 Python 采集、Go 采集、共享 UI；文件范围互不重叠。
- 范围：只读用量解析、双端 API、项目/会话/模型页面、第三方参考外链、打包清单与测试。无版本变更、安装、提交、推送或发布授权。
- 技术验证：2026-09-12 `bash tests/run_unit_tests.sh` 通过（259 Python + Go + relay + 更新辅助器 + 前端契约）；审查修正后重跑 19 项 Codex Python/UI、双端 API 各 8 项、Go 全测试及 Windows amd64 交叉编译均通过，`git diff --check` 通过。日志中的断连 BrokenPipe 和故障注入输出不导致测试失败。
- UI：本地浏览器实测真实数据渲染、项目展开、会话 ID 展示、近七天切换；截图发现长数字断行后改为桌面三列，6 项 UI 契约测试通过。原生 macOS/Windows 打包安装验收 not_run。用户体验确认 pending；发布 not_run。下方旧快照及历史测试不能代表本次代码状态。
- 审查处置：修正文件 mtime 导致漏计、Go 未归属名称/会话聚合差异、双端 OPTIONS 不一致；诊断折叠，Windows 构建同步新页面。无 ID 镜像去重歧义、标识冲突保守未归属、个人官方额度未接入已记录 README，不能把 Token 占比折算成订阅额度占比。

- 规则：MAR @ `4606982`（固定副本 `mar/`，采纳记录 `AGENTS.md`）
- 代码：`main` @ `b78cf9c7`，工作区干净；origin/GitCode 与 github 双远端同步（github 原落后 120 commit，2026-09-07 补齐）
- 发布：**v1.5.22** 已发布（2026-09-06，tag 已推 origin）；其后 3 个 commit 均为文档/memory，零业务代码变更
- 写入者：单一写入者模式（鹏帅 + 其授权的会话 Agent），无并发
- 验证有效性：业务代码测试以 v1.5.22 报告为准（7 项全过，`tests/reports/v1.5.22-pre-release-*.json`）；GOV-05 复用依据 = 此后无业务代码改动

## 活动任务（唯一台账）

| task_id | 优先级 | owner | 目标与验收条件 | 状态 | 备注/复捡条件 |
| --- | --- | --- | --- | --- | --- |
| TASK-CODEX-PROJECTS-01 | P1 | Codex | 本地 Codex 项目/模型/会话用量及重置参考入口 | pending 用户验收 | 技术验证见当前接手记录；不发布、不代订阅 |
| TASK-OPS-01 | P0 | 鹏帅+Claude | GitCode 凭据失效事故恢复（2026-09-14）：新建 PAT → 钥匙串写回 → push 通道恢复 → VPS 中继 env 更新+重启 → 补报端到端验证 | **closed（2026-09-14）** | 证据：补报 synced @06:30:36Z；聚合 total_users=19/active=1/我的用量 237123333/rank=1/榜首巴乔回显；双远端 push 4d3750c2 起恢复。附带产出：VPS（华为云 121.37.142.174，经主机密钥指纹+HTTPS 直连双重判定）装机专用密钥 `~/.ssh/vps_taqi`（root 密钥登录验证通过；sshd 配置未动）；遗留待查：直连 IP 的 SNI 场景证书链告警（curl exit 60，nginx 正常路径无此问题） |
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
