# Token Monitor 测试体系

发布前测试分为三层，不能只跑其中一层代替全部验证。

| 层级 | 命令 | 数量与目的 |
| --- | --- | --- |
| 单元测试 | `bash tests/run_unit_tests.sh` | 数量最多；覆盖解析、统计、缓存、更新、安全和前端静态契约 |
| API 契约测试 | `python3 tests/api_contract.py --backend all` | 充分覆盖 Python/macOS 与 Go/Windows 的公开接口、边界、分页、缓存和拒绝跨站写入 |
| E2E | `bash tests/e2e_ui.sh` | 只验证关键路径：首页 -> 热力图（不等待后台扫描）-> 近一年范围 |

## 发布命令

```bash
bash verify_release.sh
```

该命令严格按 Unit -> API -> E2E -> Windows 构建 -> macOS 构建执行。任一层失败立即停止，`release_all.sh` 会在创建 tag 和上传 Release 前自动调用它。

## API 测试原则

`api_contract.py` 为每个后端创建独立的临时 HOME、锁文件和热力图缓存，并使用本地更新源夹具。因此它不会读取或修改真实用户数据，也不会依赖 GitCode、VPS 或社区线上状态。

当前 API 契约覆盖：应用信息、更新检查及平台资产选择、首页统计、历史趋势、1/30/90/180/365 天热力图、热力图缓存、会话和热力图详情分页、会话详情、昵称接口的非法请求/跨站拒绝，以及未知接口 404。所有临时 API/E2E 服务都会关闭社区自动上报；单元测试额外等待超过 5 秒验证该隔离开关，避免测试数据污染线上社区。

## E2E 说明

E2E 保持少量。macOS 上需要 `npx`、Playwright CLI 和 Microsoft Edge；脚本会启动隔离的本地服务，实际点击热力图入口和“近一年”Tab，断言 UI 上的日期范围。Windows 自启、托盘和在线替换属于系统级行为，相关改动仍必须在真实 Windows 机器完成一次验收。

`tests/smoke.sh` 保留为兼容入口，会转发到新的单后端 API 契约测试；旧的 `tests/reports/` 是历史记录，不再作为发布依据。
