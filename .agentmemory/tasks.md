# 任务

> 2026-09-07 全面整理（MAR 启用后首次盘点，基线 v1.5.22 @ 2418878b）。旧"进行中"TM-2026-07-18-01 已有完成证据（events.jsonl#evt-20260718-0001 memory.synchronized），移入已完成。

## 进行中

（空 — 无在途编码任务；最近发版 v1.5.22 已完成，tag 已推 origin）

## 待处理

### P0 安全
- ~~TM-2026-09-07-01：`AGENTS.md` 明文 GitCode token 迁移+轮换~~ — **已关闭**：鹏帅 2026-09-07 明确指示忽略（见 `decisions.md#D-2026-09-07-01`），作为项目已接受风险，不再复提。

### P1 同步与仓库卫生 —— **已全部完成（2026-09-07，鹏帅指令"请处理 P1 的内容"）**
- ✅ TM-2026-07-18-03：push `github` 远端（实测落后 120 commit + 本次新增 2 commit，已推送；origin 一并同步）。
- ✅ TM-2026-09-07-02：工作区处置 — ① memory/MAR 文件已提交；② 两个二进制判定为 9/6 冒烟临时构建（源码零改动、最后入库 v1.5.02、近 20 版惯例不入库），备份 `/tmp/tm-backup-*-20260907` 后恢复 HEAD；③ `.zcode/plans/plan-sess_521e2754*` 按其他 plan 已跟踪惯例补提交（未删除）。
- ✅ TM-2026-09-07-03：记忆同步完成 — `current-state.md` 重写至 v1.5.22（v1.4.53~v1.5.22 共 57 版按主题分组摘要），`brief.md` 补 MiniMax Code 数据源 + 当前阶段重写。

### P2 产品候选（继承自 v1.4.42 候选期，2026-09-07 复核仍未做）
- TM-2026-07-18-02a：更新进度 UI 文案简化与双端对齐 — macOS `app_wrapper.swift` 仍显示"下载更新包 (…)"，Windows `update_windows.go` 已是"下载中 x%"，两端文案不一致（实测 grep 确认）。发版需鹏帅明确说"发新版本"。
- TM-2026-07-18-02b：CDN 占位 retry 不稳定（同 IP 路由到 download-error 占位，retry 1 次仍可能失败）。v1.5.15/16 DMG 附件主路径已缓解主要事故面，降级为已知限制，择机优化。

### 记忆与知识
- TM-2026-07-18-04：接入 AgentMemory 阶段 3，把 `.codex/project_memory.md` 中"事实"蒸馏进 `.agentmemory/decisions.md`（非任务相关，**不**自动执行）。

### 暂缓（复捡条件触发式，需鹏帅发起）
- TM-2026-07-30-01：TRAE IDE 接入暂缓（`decisions.md#D-2026-07-30-01`）。复捡条件：① 社区出现 macOS Trae CN SQLCipher 密钥提取方案；② 鹏帅确认 Trae 可走 cc-switch 代理需验证间接统计；③ 鹏帅决定做 Windows-only 扫描器（接受双端不对齐）。
- TM-2026-08-27-01：豆包工作接入暂缓（`decisions.md#D-2026-08-27-01`）。复捡条件：① 豆包工作本地落盘 token 数据或官方导出；② 字节开放企业用量 API 且鹏帅认可；③ AgentsView 官方支持。

### 长期已知边界
- SMAppService daemon 注册（需 Apple Developer Account，未做）。
- `go_build/` 在 macOS 端运行时不使用但保留（Windows 构建源，不删）。

## 已完成

- TM-2026-07-18-01：memory 同步 v1.4.32→v1.4.41 —— 证据：events.jsonl#evt-20260718-0001 memory.synchronized（2026-09-07 盘点时补记）
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
