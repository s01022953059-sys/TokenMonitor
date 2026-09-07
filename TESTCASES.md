# token_monitor 测试与缺陷

> 按 MAR GOV-04 实例化（2026-09-07，模板 @ 4606982）。本文件维护用例索引与缺陷记录；结果与发布状态统一在 `HANDOFF.md`，不复制另一份可变看板。用例实体在 `tests/`（Python）与 `go_build/*_test.go`（Go），本表只做索引，不镜像实现。

## 验证策略

按风险分层：单元（`tests/run_unit_tests.sh`）→ API 契约（`tests/api_contract.py`）→ 浏览器 E2E（`tests/e2e_ui.sh`）→ 冒烟（`tests/smoke.sh`，发版前必跑完整套件，见 REQ-04）→ 双端构建（`build_macos.sh`/`build_windows.sh`）。业务代码修复必须在同一提交补测试用例（AGENTS.md 约定 7）。

| case_id | requirement_id / bug_id | 覆盖内容（索引，不复制断言） | 执行方式 | 证据 |
| --- | --- | --- | --- | --- |
| TC-A | REQ-01/06/07/08 | 数据源口径（Codex/cc-switch/WorkBuddy/Antigravity/Hermes/MiniMax/NewSources）、缓存语义、社区隔离、安全、性能阈值 | `tests/run_unit_tests.sh` + `tests/api_contract.py` + `go_build` Go 测试 | `tests/reports/index.json` |
| TC-B | REQ-02 | UI 无未采集占位字段（随功能用例覆盖，新增维度进 UI 前须过此项人工审查） | 人工 + E2E | 见 HANDOFF 验收记录 |
| TC-C | REQ-03/08 | 浏览器实测：排名动画、热力图、图例对齐、大会话详情性能 | `tests/e2e_ui.sh` + 真实浏览器 | `tests/reports/` |
| TC-D | REQ-04 | 发版门禁：`release_all.sh` 内部先跑 `verify_release.sh`（Unit→API→E2E→构建）全过才进 tag/Release | `bash verify_release.sh` | 发版报告归档 commit |
| TC-E | REQ-05 | 自更新端到端：首选资产 = DMG（跳过 type=source）、挂载校验内含 .app 版本、旧 Foundation 空格 URL | `release_all.sh` 上传后校验 + macOS 实机更新 | v1.5.21/22 报告 |

## 缺陷记录

- **BUG-01（open，已降级为已知限制）**：CDN 占位 retry 不稳定 — GitCode CDN 对某些 IP 路由到 download-error 占位页（<10KB），retry 1 次仍可能拿到同占位。影响面已被 v1.5.15/16 DMG 附件主路径缓解；残余场景用户多点几次可恢复。根因在 GitCode CDN 侧，无法本地修复。关联：TM-2026-07-18-02b。
- **BUG-02（won't-fix，文档化）**：macOS v1.5.13 及更早客户端无法应用内自更新 — 其资产选择器永远命中 `type=source` 源码归档（对 tag 发布必 302 到占位页）。修复方式：用户手动下载 DMG 升级一次即进入 v1.5.15+ 正常链路；Windows 不受影响。关联：`docs/events.jsonl#evt-20260827-0001`。

## 结果

- 最近一次发版验证：**v1.5.22**（2026-09-06）7 项全过（含浏览器对齐实测），报告 `tests/reports/v1.5.22-pre-release-2026-09-06T13-29-57Z.json`，归档 commit `54f7f47f`。
- 当前 HEAD（`b78cf9c7` 起）相对 v1.5.22 仅文档/memory 变更，业务代码零改动 → 按 GOV-05"能证明无关的检查可复用"，v1.5.22 报告对业务代码仍有效；下次业务代码变更或发版前须重跑冒烟（默认 not_run）。
