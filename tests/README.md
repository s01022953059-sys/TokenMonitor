# Token Monitor 冒烟测试

发版前必跑。所有测试通过才允许 release。

## 怎么跑

```bash
# 测当前 dev source (Python server)
bash tests/smoke.sh

# 测 Go build (Mac 二进制, 默认是 /tmp/test_TM_mac)
bash tests/smoke.sh --go

# 测指定版本 (label 给 reports 文件名)
bash tests/smoke.sh --version v1.3.92
```

跑成功 → 自动更新 `tests/reports/index.json` (索引) + `tests/reports/index.html` (可视化) 一起入库。

## 看结果

```bash
open tests/reports/index.html
```

显示 3 块信息: 最新一次通过率 / 失败数 / 历史报告数 + 每份报告详情 (PASS/FAIL 颜色, 时间戳, 各用例结果)。

## 测试用例 (9 条)

| # | 名称 | 验证 |
|---|---|---|
| 1 | /api/app-info 返 200 | 返 version / update_enabled |
| 2 | /api/usage by_tool 含 Codex/Claude/Hermes/OpenCode | 4 个真实工具都在 |
| 3 | /api/history by_model Other < 5% | 防 v1.3.92 累加 bug 复发 |
| 4 | /api/history labels = 30 | days=30 参数工作 |
| 5 | /api/heatmap days=30 返 days 数组 | 30 天数据齐 |
| 6 | /api/check-update 返 ok+version | 升级检测 |
| 7 | /api/sessions 返 sessions + total | 会话列表 API |
| 8 | /api/heatmap_detail date=今天 返 sessions + summary | 详情 API |
| 9 | server 监听 0.0.0.0:15723 | 进程存活 |

## 报告文件

- `tests/reports/<label>-pre-release-<ISO8601>.json` - 每次跑一份 (git tracked)
- `tests/reports/index.json` - 全部报告索引 (git tracked)
- `tests/reports/index.html` - 可视化页面 (git tracked, 直接打开)

## 怎么加新测试

在 `tests/smoke.sh` 里加一行 `run_test "..." "..." "..."` (三参数: 名字, curl cmd, python assert).

例:

```bash
run_test "/api/health 返 ok" \
    'curl -s http://localhost:15723/api/health -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert d.get("ok")==True'
```

## CI 集成 (未来)

GitHub Actions `windows-latest` 跑:
- `bash tests/smoke.sh` (Python server, 测主项目)
- `bash build_windows.sh && bash tests/smoke.sh --go` (Go server, 测 Win 版本)
- 全过 → 自动 commit 报告回 main 分支

Mac 端 `macos-latest` 跑 (用 launchd 启动 launcher + 截图 UI):
- UI 点击 4 Tab (周/月 × 工具/模型)
- 验证状态栏 🔥 显示
- 验证主窗口正常加载

## 历史教训

- **不要** 在 expect 用 f-string (bash heredoc 转义会把 `\` 吃了). 用 `+ str()` 拼接:
  ```python
  # 错 (test 4 f-string bug):
  assert len(d.get("labels",[]))==30, f"expected 30 labels, got {len(d.get(\"labels\",[]))}"
  # 对:
  assert len(d.get("labels",[]))==30, "expected 30 labels, got " + str(len(d.get("labels",[])))
  ```
- **不要** 在 bash heredoc 用 array expansion 传复杂 JSON, 用文件传:
  ```bash
  # 错 (REPORTS 元素含 | 乱):
  for line in '''${REPORTS[@]}'''.split("\n"): ...
  # 对:
  cat $f > /tmp/x; python3 -c "open('/tmp/x')..."
  ```

## 跟 release-batching / release-checklist 的关系

- **release-batching.md** - 改完 bug 不要立刻发, 等用户回复"发"
- **release-checklist.md** - 发版前必做的 5 项代码检查
- **smoke-tests.md** (本目录) - 发版前必跑的功能验证 (运行时)
- **三者** 都不冲突, 顺序: batching (要不要发?) → checklist (代码干不干净?) → smoke-tests (功能能不能用?) → release
