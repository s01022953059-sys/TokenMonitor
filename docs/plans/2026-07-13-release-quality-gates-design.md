# 发布质量门禁设计

## 目标

把发布验证拆成互不混淆的三层：大量、快速、确定的单元测试；覆盖双端后端的 API 契约测试；以及极少量真实用户路径 E2E。所有层级都必须通过，才允许构建、打 tag 和上传 Release。

## 分层

| 层级 | 命令 | 责任 | 失败策略 |
| --- | --- | --- | --- |
| Unit | `bash tests/run_unit_tests.sh` | 解析、去重、统计、缓存、更新资产、安全规则、前端静态契约 | 立即停止 |
| API | `python3 tests/api_contract.py --backend all` | Python/macOS 与 Go/Windows 对同一 HTTP 契约的响应结构、范围、分页、缓存、更新与拒绝跨站写入 | 立即停止 |
| E2E | `bash tests/e2e_ui.sh` | 首页打开热力图并切换近一年 | 立即停止；涉及 Windows 系统行为时加真实 Windows 人工验收 |

## API 契约

测试通过临时 HOME、临时锁和临时缓存启动两个本地后端，不读取真实用户日志、不写社区数据。更新检查使用本地 HTTP 夹具，不依赖 GitCode 或公网。覆盖 `/api/app-info`、`/api/check-update`、`/api/usage`、`/api/history`、`/api/heatmap`、`/api/sessions`、`/api/heatmap_detail`、`/api/session_detail` 和昵称接口的无效/跨站请求。

## 发布顺序

`verify_release.sh` 负责按 Unit -> API -> E2E -> 构建的顺序执行。`release_all.sh` 只能调用该总入口，只有它成功后才允许创建 tag、构建附件和上传。版本号、提交、tag、Release 仍须由鹏帅明确要求后才执行。
