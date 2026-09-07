# 当前状态

更新时间：2026-09-07（MAR 启用 + P1 遗留任务处理时全面同步；上次快照停在 2026-07-25 / v1.4.52）

## 正在进行

- 发布基线 **v1.5.22**（tag commit `15178a17`），origin/GitCode 与 `github` 双远端已同步（github 原落后 120 commit，2026-09-07 补齐；其后新增文档 commit 亦已推送）
- MAR（Multi-Agent-Rule）已于 2026-09-07 启用并完成四文档重构：`mar/` @ `4606982` 固定副本；`AGENTS.md`（指引）/ `PRD.md`（REQ-01~08）/ `TESTCASES.md`（TC/BUG）/ `HANDOFF.md`（交接+活动任务唯一台账），本目录 `.agentmemory/` 保留决策/版本史/事件/归档职责
- 无在途编码任务；工作区干净（9/6 冒烟临时重建的二进制已恢复 HEAD 版本，备份 `/tmp/tm-backup-*-20260907`）

## v1.4.53 → v1.5.22 版本史摘要（57 次发版，按主题分组）

- **排名趋势图表系列**（v1.4.56~v1.4.72）：对数 Token 刻度、折线连续性与零值不断线、标签重叠/越界/边缘裁切修复、零用量排名补全 + 区间总榜、跟随系统主题
- **图表刻度与轴**（v1.4.73~v1.4.81）：y 轴 10 的幂次等距刻度、线性+对数补丁拉大差距、x 轴中文日期标签、月初无归档修复、自定义刻度异常修复
- **图例指标与 macOS 构建**（v1.4.82~v1.4.88）：图例二级菜单独立指标、聚合字段补齐修复 NaN、macOS universal binary（Intel + Apple Silicon）、更新后首页全 0 修复、社区成员统计丢失/波动修复
- **组队排名功能**（v1.5.0~v1.5.10）：组队排名、一人多组、极简化组队 UI、页面内加入 + 组内排名 + 零网络调用、Top10 按组 Tab 筛选、加入组队卡住/无响应多轮修复
- **MiniMax Code 数据源**（v1.5.11~v1.5.13）：SQLite v2 主源 + JSONL 兜底 + `mvs_` turn_id 去重、模型名剥 `custom_provider:`/`custom-local:` 前缀、Windows Go 端同步
- **macOS 自动更新大修**（v1.5.14~v1.5.18）：GitCode `type=source` 归档必 302 到占位页 → 改 DMG 附件挂载安装（hdiutil+ditto）、旧 Foundation 空格 URL `%20` 兼容、跨平台加入组修复、双副本版本误读、macOS 27 立即更新必崩修复
- **统计口径与新数据源**（v1.5.19~v1.5.20）：工具/模型 Other 合并规则统一 <0.1%、更新下载兜底 + Antigravity 数据源
- **收尾**（v1.5.21~v1.5.22）：DMG 自动更新挂载死锁 + plist 新格式、图例数字/百分比中轴对齐

## 下一步

活动任务与下一步**唯一台账见 `HANDOFF.md`**（GOV-04 不复制可变状态）；本文件只维护版本史与项目状态事实。

## 已知边界

- SMAppService daemon 注册（需 Apple Developer Account，**未做**）
- `go_build/` 目录 macOS 运行时不用但保留（Windows 构建源）
- CDN 占位 retry 不稳定（同 IP 路由到 download-error 占位；v1.5.15/16 DMG 主路径已缓解主要事故面）
- macOS v1.5.13 及更早客户端无法应用内自更新（选择器命中损坏源码归档），需手动下载 DMG 升级一次；Windows 不受影响
- 修复统计口径前**禁止**用"看起来差不多"的字段名硬猜；不确定就查 AgentsView
- `AGENTS.md` 明文 GitCode token：鹏帅 2026-09-07 决定保持现状（`decisions.md#D-2026-09-07-01`），不再复提
