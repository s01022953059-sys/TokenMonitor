# 当前状态

更新时间：2026-07-25（增量记录；基础摘要仍需后续整体同步）

## 正在进行

- 发布基线为 v1.4.41（HEAD `fd17b1c`），9 个 tag `v1.4.33`~`v1.4.41` 已在 GitCode `origin/main` 同步；本机 10 个本地 commit 领先 `github/main`，**未** push
- 本次 memory 同步：把 `.agentmemory/` 从 v1.4.32 快照推进到 v1.4.41，补 fork/缓存/性能门禁/数据源/企业代理等关键决策
- 工作区除本目录内存同步修改外已 commit 干净；仅剩 `.claude/` untracked（Claude Code 适配器目录，**不**自动提交）
- 当前代码基线实际为 v1.4.52（HEAD `afe4ac9`）。TOP10 排名变化的未发布体验改造已完成：四段时间范围替代日期箭头和播放按钮，打开或切换范围后从最早帧自动播放到最新帧并停止；macOS/Windows 后端均支持 `days=30/90/180/365`。106 项 Python/前端/辅助器测试与 Go、社区中继回归全部通过，真实浏览器三帧验收确认排名会自动换位且零控制台错误；尚未 bump、commit、tag 或发布。

## 已有产出

- macOS 应用壳：`app_wrapper.swift`
- Python 后端：`scanner.py`（数据采集）、`server.py`（HTTP API，含 `server.py --heatmap-worker` 子进程模式）
- Windows 端：`go_build/main.go` + `build_windows.sh` + 安装程序
- 社区功能：`community.py` + `community/` 目录 + `community_dashboard.html` + `community_relay`
- 测试：`tests/`（unit / api_contract / e2e；新增 `test_heatmap_detail_cache.py`、`test_heatmap_detail_filter.py`、`test_heatmap_detail_range.py`、`test_scanner_accuracy.py`、`test_codex_scanner.py`、`test_usage_cache.py` 等）
- 发版脚本：`verify_release.sh` + `release_all.sh` + `build_dmg.sh` + `build_macos.sh`
- 文档：`README.md`（必须与代码同步）、`AGENTS.md`（接手规则）、`docs/PROJECT_STATUS.md`（项目状态全量）
- Codex 长期记忆：`.codex/project_memory.md`（28 KB 全量原文，未迁入 `.agentmemory/`）

## 下一步

1. 鹏帅先体验 TOP10 排名变化的新交互；确认后如需发版，再单独授权版本号、commit、tag 与 Release。
2. 鹏帅明确说"发新版本"后才能发布新版本；普通修复**不**改版本号、**不**打 tag、**不**推 Release
3. 评估后续候选：UI 文案简化（"下载更新包" → "下载中"）、CDN 占位 retry 不稳定
4. 接入 AgentMemory 阶段 3：可考虑把 `.codex/project_memory.md` 中"事实"蒸馏进 `.agentmemory/decisions.md`（非任务相关，**不**自动执行）
5. 同步尚未推送到 `github` 的本地提交（不涉及发版，仅同步）

## 已知边界

- SMAppService daemon 注册（需 Apple Developer Account，**未做**）
- `go_build/` 目录在 macOS 端不使用但**未**删
- CDN 占位 retry 仍不稳定（同 IP 路由到 download-error 占位）
- 修复统计口径前**禁止**用"看起来差不多"的字段名硬猜；不确定就查 AgentsView
- v1.4.41 后无新功能发版，调用详情筛选与缓存口径在 v1.4.42 候选期需保持稳定
