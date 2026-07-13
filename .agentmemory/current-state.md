# 当前状态

更新时间：2026-07-13（轻接入 AgentMemory）

## 正在进行

- 准备 v1.4.33：修复社区用量同步提示、测试报告隔离、年度热力图对齐
- 工作区有 7 个 M 文件（`go_build/static/index.html`、`index.html`、`scanner.py`、`server.py`、`tests/api_contract.py`、`tests/e2e_ui.sh`、`tests/run_unit_tests.sh`）+ 3 个 ??（`.claude/`、`tests/test_heatmap_detail_cache.py`、`tests/test_heatmap_detail_range.py`），与本任务无关，**不**自动提交

## 已有产出

- macOS 应用壳：`app_wrapper.swift`
- Python 后端：`scanner.py`（数据采集）、`server.py`（HTTP API）
- Windows 端：`go_build/main.go` + `build_windows.sh` + 安装程序
- 社区功能：`community.py` + `community/` 目录 + `community_dashboard.html`
- 测试：`tests/`（unit / api_contract / e2e）
- 发版脚本：`verify_release.sh` + `release_all.sh` + `build_dmg.sh` + `build_macos.sh`
- 文档：`README.md`（必须与代码同步）、`AGENTS.md`（接手规则）、`docs/PROJECT_STATUS.md`（项目状态全量）
- Codex 长期记忆：`.codex/project_memory.md`（28 KB 全量原文，未迁入 `.agentmemory/`）

## 下一步

1. 鹏帅在 v1.4.32 基础上继续 v1.4.33 的本地修复
2. 修复完成后按 `AGENTS.md` 流程同步刷新 `README.md`
3. 鹏帅明确说"发新版本"后再 bump 版本号 + tag + 跑 `verify_release.sh` + `release_all.sh`
4. 接入 AgentMemory 阶段 3 后，可考虑把 `.codex/project_memory.md` 中"事实"蒸馏进 `.agentmemory/decisions.md`（非任务相关，**不**自动执行）

## 已知边界

- UI 文案简化（"下载更新包" → "下载中"等）**未改**
- SMAppService daemon 注册（需 Apple Developer Account，**未做**）
- `go_build/` 目录在 macOS 端不使用但**未**删
- CDN 占位 retry 仍不稳定（同 IP 路由到 download-error 占位）
- 修复统计口径前**禁止**用"看起来差不多"的字段名硬猜；不确定就查 AgentsView
