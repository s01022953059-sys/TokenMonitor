# token_monitor 历史档案

> 2026-09-07 按鹏帅指示删除 `.agentmemory/`（AgentMemory 轻接入），信息全部迁入 MAR 文档体系。本文件承载**历史档案**（按需读取，不挤占日常上下文）；活动任务在 `HANDOFF.md`，业务规格在 `PRD.md`，决策在 `docs/DECISIONS.md`，事件审计在 `docs/events.jsonl`。

## .agentmemory 迁移映射（信息零丢失对照表）

| 原文件 | 去向 | 说明 |
| --- | --- | --- |
| `decisions.md` | `docs/DECISIONS.md` | 原文逐字迁移（D-xxxx 稳定 ID 不变，全库引用已更新） |
| `events.jsonl` | `docs/events.jsonl` | 原文逐字迁移（11 条，行数校验一致），后续事件继续追加 |
| `current-state.md` | 本文件"版本史"+"已知边界"；当前快照归 `HANDOFF.md` | 重写于 2026-09-07（v1.5.22 基线） |
| `tasks.md` | 本文件"已完成任务归档"；活动任务归 `HANDOFF.md` | — |
| `brief.md` | `PRD.md`（速览/数据源/术语并入 REQ 与附录） | 数据源明细已内联，不再依赖原文件 |
| `glossary.md` | `PRD.md#附录-术语表` | 过时条目（v1.4.4x 基线、轻接入）已剔除/改写 |
| `manifest.yaml` | `docs/archive/agentmemory-manifest.yaml` | 原样归档（工具配置，policies 已由 REQ-04/DECISIONS 承接） |
| `inbox/knowledge-updates.jsonl` | `docs/archive/agentmemory-inbox-knowledge-updates.jsonl` | **原未入 Git**，13 条知识更新审计（12 applied + 1 proposed 未确认 KU-5CE512BC），原样归档 |
| `index.sqlite3` | 不入库（派生索引，gitignore 指定） | 留底 `/tmp/tm-agentmemory-index-20260907.sqlite3`，可由 AgentMemory 工具重建 |

**双保险恢复**：删除前全部内容已提交于 Git（commit `3571278c`，origin+github 双远端），任意文件可 `git show 3571278c:.agentmemory/<file>` 找回。

## 版本史摘要（v1.4.53 → v1.5.22，57 次发版，按主题分组）

- **排名趋势图表系列**（v1.4.56~v1.4.72）：对数 Token 刻度、折线连续性与零值不断线、标签重叠/越界/边缘裁切修复、零用量排名补全 + 区间总榜、跟随系统主题
- **图表刻度与轴**（v1.4.73~v1.4.81）：y 轴 10 的幂次等距刻度、线性+对数补丁拉大差距、x 轴中文日期标签、月初无归档修复、自定义刻度异常修复
- **图例指标与 macOS 构建**（v1.4.82~v1.4.88）：图例二级菜单独立指标、聚合字段补齐修复 NaN、macOS universal binary（Intel + Apple Silicon）、更新后首页全 0 修复、社区成员统计丢失/波动修复
- **组队排名功能**（v1.5.0~v1.5.10）：组队排名、一人多组、极简化组队 UI、页面内加入 + 组内排名 + 零网络调用、Top10 按组 Tab 筛选、加入组队卡住/无响应多轮修复
- **MiniMax Code 数据源**（v1.5.11~v1.5.13）：SQLite v2 主源 + JSONL 兜底 + `mvs_` turn_id 去重、模型名剥 `custom_provider:`/`custom-local:` 前缀、Windows Go 端同步
- **macOS 自动更新大修**（v1.5.14~v1.5.18）：GitCode `type=source` 归档必 302 到占位页 → 改 DMG 附件挂载安装（hdiutil+ditto）、旧 Foundation 空格 URL `%20` 兼容、跨平台加入组修复、双副本版本误读、macOS 27 立即更新必崩修复
- **统计口径与新数据源**（v1.5.19~v1.5.20）：工具/模型 Other 合并规则统一 <0.1%、更新下载兜底 + Antigravity 数据源
- **收尾**（v1.5.21~v1.5.22）：DMG 自动更新挂载死锁 + plist 新格式、图例数字/百分比中轴对齐

逐版 commit 以 `git log --oneline v1.4.52..v1.5.22` 为准；v1.4.52 以前历史见 `docs/PROJECT_STATUS.md` 与 Git。

## 已知边界（长期有效）

- SMAppService daemon 注册需 Apple Developer Account，**未做**（台账 TM-LEGACY-01）
- `go_build/` 目录 macOS 运行时不使用但保留（Windows 构建源，不删）
- CDN 占位 retry 不稳定 = `TESTCASES.md#BUG-01`（已降级已知限制）
- macOS ≤v1.5.13 客户端无法应用内自更新 = `TESTCASES.md#BUG-02`（需手动 DMG 升级一次）
- 统计口径**禁止**按字段名硬猜，不确定就查 AgentsView（= PRD REQ-01）
- 二进制构建产物惯例：v1.5.03 起重建二进制不入库（9/6 冒烟构建已恢复 HEAD，备份 /tmp）

## 已完成任务归档

### 2026-09-07（MAR 启用 + P1 处理 + 文档重构 + AgentMemory 迁移）

- TASK-MAR-01：MAR 启用 — `mar/` 固定副本（7 文件 SHA-256 对齐 `4606982`）、`AGENTS.md` 采纳节、`HANDOFF.md` 实例化。commit `dd1636e8`
- TASK-MAR-02：遗留任务全面盘点 — v1.4.42 候选复核（双端文案实测仍不一致）、github 落后实测 120 commit
- TASK-P1-01：P1 三项 — github push（120+2）、工作区处置（二进制恢复/plan 补提交）、memory 同步至 v1.5.22。commit `7a874d06`+`b78cf9c7`
- TASK-MAR-03：MAR 四文档重构 — 新建 `PRD.md`+`TESTCASES.md`，HANDOFF 改唯一台账。commit `3571278c`
- TASK-MAR-04：AgentMemory 迁移删除 — 本文件所在批次（映射表见上），证据 `docs/events.jsonl#evt-20260907-0004`

### 已关闭（不执行）

- TM-2026-09-07-01：明文 GitCode token — 鹏帅决定忽略（`docs/DECISIONS.md#D-2026-09-07-01`），不再复提

### 历史（AgentMemory 时期）

- TM-2026-07-18-01：memory 同步 v1.4.32→v1.4.41 — 证据 `docs/events.jsonl#evt-20260718-0001`
- TM-000：v1.4.32 commit（`1da6bfb`）— 改善社区用量同步提示
- TM-AM-001：接入 AgentMemory 轻接入（建包 `5eb3fa3`+`6018f74`，双 remote push，context top-1 命中验证）— 证据 `docs/events.jsonl` 前 3 条
- TM-AM-002：`agentmemory context` 按项目根路由（3 场景实测）— 证据 `AgentMemory/.agentmemory/events.jsonl#evt-20260713-0015` + 本地 `#evt-20260713-0014`
- TM-2026-07-18-10~19：v1.4.33~v1.4.41 发布系列（9 版）+ 性能抖动阈值放宽（`fd17b1c`）

> 注：AgentMemory 工具（`agentmemory context` CLI）依赖 `.agentmemory/` 目录路由，删除后本项目不再被该工具发现；如未来需要可基于本档案重建。
