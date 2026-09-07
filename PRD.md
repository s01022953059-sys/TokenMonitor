# token_monitor 需求规格

> 按 MAR GOV-04 实例化（2026-09-07，模板 @ 4606982）。本文件维护业务规格与验收要求；需求验收状态与证据统一在 `HANDOFF.md`，用例与缺陷在 `TESTCASES.md`，决策依据在 `docs/DECISIONS.md`，历史档案在 `docs/HISTORY.md`，不互相复制。

## 目标与边界

- **用户**：本机运行多个 AI 编码工具（Codex、Claude Code、WorkBuddy、cc-switch、Antigravity、Hermes、MiniMax Code 等）的开发者，需要跨工具 token 用量统计与可视化。
- **业务价值**：本地只读扫描各工具日志 → 统一口径统计 → Web 控制台展示（趋势/热力图/调用详情/排名）+ 可选社区匿名排行。
- **非目标**：不做云端聚合服务；不读取需要进程内存取证/解密的数据源（TRAE、豆包工作，见暂缓决策）；不展示未采集字段。
- **已知约束**：轻量本地只读定位；macOS/Windows 双端功能必须对齐；发布权仅鹏帅。

## 业务模型与功能域

| requirement_id | 功能域 | 行为与边界 | 验收条件 | 用例索引 |
| --- | --- | --- | --- | --- |
| REQ-01 | 统计正确性 | 各数据源按既定口径采集（明细见附录 A"数据源口径"）；口径不确定时参考 AgentsView，**禁止**按字段名猜测 | 单元 + API 契约通过；真实数据验证须含 ≥100MB 本机会话夹具 | TC-A |
| REQ-02 | 不展示未采集字段 | 拿不到值的维度不进 UI（列表/弹窗/KPI）；部分采集须标注采样率（决策 `D-2026-07-31-01`） | UI 无恒为 `-`/`0` 的占位列 | TC-B |
| REQ-03 | 双端对齐 | macOS（Swift+Python）与 Windows（Go 单体）功能一致，共用 `index.html`+`chart.js`；后端逻辑改动必须双端同步（v1.5.13 为反面案例：macOS 修复未同步 Go） | 双端各自测试通过 + 浏览器实测一致 | TC-A/TC-C |
| REQ-04 | 发布策略 | 仅鹏帅明确说"发新版本"才 bump/tag/Release；bug 攒批发版；发版前完整冒烟测试全过；不自动 install 到 ~/Applications（用户亲自验证热更新） | `release_all.sh` 内含 `verify_release.sh` 门禁；HANDOFF 发布记录齐全 | TC-D |
| REQ-05 | 自更新链路 | macOS：只信 Release attach 附件（跳过 `type=source`），DMG 挂载安装（hdiutil+ditto），空格 URL→`%20`；Windows：安装程序。失败保留可恢复版本 | 端到端校验：首选资产为 DMG + 挂载内含 .app 版本号一致 | TC-E |
| REQ-06 | 社区与组队 | 昵称与匿名 ID 分离；24h 最多 3 次改名、30 天旧名保护；组队排名（一人多组、页面内加入、组内排名零网络调用）；测试报告与社区数据三处口径隔离 | 社区单元/隔离测试通过 | TC-A |
| REQ-07 | 企业网络兼容 | 所有外部请求遵循系统代理与 `HTTP(S)_PROXY`/`NO_PROXY`；本机回环 API 不经代理 | 代理场景测试通过 | TC-A |
| REQ-08 | 性能门禁 | 会话详情首开 <500ms、二次 <300ms（E2E <1s/<300ms）；每日详情按自然日限界扫描 + 缓存切片，不重扫日志；macOS **禁止**多线程服务内 fork（重扫描走 `server.py --heatmap-worker` 独立进程） | API 契约阈值测试 + E2E 通过 | TC-A/TC-C |

## 非功能要求

- **安全与数据流**：本地日志只读，数据不出本机（社区同步除外，匿名化）；凭据问题按 `docs/DECISIONS.md#D-2026-09-07-01`（鹏帅已接受现状）。
- **支持平台**：macOS（universal：Intel+Apple Silicon，v1.4.87 起）/ Windows；macOS 27 兼容（v1.5.18/21 修复）。
- **恢复**：单实例内核独占锁，不删残留锁文件；SQLite 只读连接 + busy timeout + 3 次短重试，禁止单次失败归 0。

## 变更影响

- 新缺陷改变业务定义时：更新本文件对应 REQ + `TESTCASES.md` 缺陷记录 + 链接 `docs/DECISIONS.md` 决策 ID。
- 暂缓数据源（TRAE `D-2026-07-30-01`、豆包工作 `D-2026-08-27-01`）复捡时：先补 REQ 再实施。

## 附录 A：数据源口径（2026-09-07 自原 brief.md 迁入）

- **Codex**：`logs_2.sqlite` + rollout JSONL 始终合并去重（漏统已修，v1.4.21）
- **Claude Code**：原生日志路径
- **WorkBuddy**：`~/.workbuddy/projects/**/*.jsonl` 的 `providerData.usage` 逐请求（v1.4.22 审计后落地）；旧版缺 projects 时回退 `workbuddy.db` 会话占用
- **cc-switch**：`~/.cc-switch/cc-switch.db`（OpenAI input 已含 cache、Anthropic input 不含 cache read/create，按协议语义分别计算）
- **Antigravity**：`~/Library/Application Support/BingchaAI/usage_stats.json`（macOS 专属，v1.5.20 接入）
- **MiniMax Code**：`~/.minimax/v2/sqlite/runtime-state.sqlite`（SQLite 主源）+ JSONL 兜底，`mvs_` 前缀 turn_id 去重；模型名剥 `custom_provider:`/`custom-local:` 前缀（v1.5.11~v1.5.13，双端同步）
- **Hermes**：`~/.hermes/state.db`（输入 = input + cache_read + cache_write；用量日期采用 `ended_at`）
- **缓存语义**：区分"请求内缓存"与"跨请求缓存"，避免重复计费
- **健壮性**：本地 SQLite 在 WAL/原子替换瞬间可能短暂打不开，统一只读连接 + busy timeout + 3 次短重试，禁止单次失败直接归 0

## 附录 B：术语表（2026-09-07 自原 glossary.md 迁入，剔除过时条目）

- **token monitor**：跨平台 AI/Agent 用量统计与可视化工具，本项目
- **AgentsView**（`kenn-io/agentsview`）：统计口径参考仓库；字段含义不确定时优先查这里
- **logs_2.sqlite**：Codex 本地历史日志库；与 rollout JSONL 合并后才算全量 usage（v1.4.21 起）
- **rollout JSONL**：Codex 每次会话/请求的逐次事件流，含 token 用量、模型、缓存命中
- **单实例锁**：本地端口独占保护；以**内核独占锁为准**，不删残留锁文件
- **年度热力图**：年度内每日用量的网格可视化，需和"本地总量"与"社区用量"对齐
- **社区昵称**：用户公开昵称；与匿名 ID 分离，24h 3 次改名上限，30 天旧名保护
