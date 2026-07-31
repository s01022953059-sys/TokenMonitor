# 任务

## 进行中

- TM-2026-07-18-01：memory 同步 v1.4.32→v1.4.41（本次）：补 brief.md / current-state.md / decisions.md / tasks.md / events.jsonl，**不**改业务文件、**不**授权发版

## 待处理

- TM-2026-07-18-02：评估 v1.4.42 候选修复（UI 文案简化、CDN 占位 retry 稳定性）—— 需要鹏帅明确说"发新版本"才能发布
- TM-2026-07-18-03：把 10 个本地 commit push 到 `github` 远端（仅同步，不涉及发版授权）
- TM-2026-07-18-04：接入 AgentMemory 阶段 3，把 `.codex/project_memory.md` 中"事实"蒸馏进 `.agentmemory/decisions.md`（非任务相关，**不**自动执行）
- TM-2026-07-30-01：调研 TRAE IDE 接入可行性（已调研，结论见 `decisions.md#D-2026-07-30-01`）。当前**暂缓**；触发复捡条件之一：① 社区出现 macOS 上 Trae CN SQLCipher 密钥提取方案；② 鹏帅确认 Trae 能配置走 cc-switch 代理并需验证间接统计效果；③ 鹏帅决定先做 Windows-only Trae CN 扫描器（需接受两版不对齐）。**不**自动执行，需鹏帅发起。

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
- TM-2026-07-18-10：v1.4.33 发布（`bbc17d2`）—— 加速每日调用详情 + About 展示更新摘要
- TM-2026-07-18-11：v1.4.34 发布（`6e2c50c`）—— macOS fork 崩溃修复（切 `server.py --heatmap-worker` 子进程）+ 统一桌面体验 + 优化社区统计
- TM-2026-07-18-12：v1.4.35 发布（`65ce07b`）—— 修复 macOS Python 崩溃 + 优化社区体验
- TM-2026-07-18-13：v1.4.36 发布（`784c81f`）—— 修复调用详情缓存复用 + 社区指标文案
- TM-2026-07-18-14：v1.4.37 发布（`17d5a29`）—— 修复大型会话详情加载 + 双端缓存复用
- TM-2026-07-18-15：v1.4.38 发布（`ab36244`）—— 修复 macOS 高 CPU + 退出后端残留
- TM-2026-07-18-16：v1.4.39 发布（`dd50406`）—— 修复热力图详情最后一页分页
- TM-2026-07-18-17：v1.4.40 发布（`e3d9f92`）—— 首页圆环新增调用次数 + 缓存命中率
- TM-2026-07-18-18：v1.4.41 发布（`920d78f`）—— 调用详情增加组合筛选
- TM-2026-07-18-19：test commit（`fd17b1c`）—— 放宽跨平台 API 性能抖动阈值
