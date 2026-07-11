# Token Monitor 项目记忆

## 核心约定

1. **称呼用户为"鹏帅"**
2. **每次更新代码后同步刷新 README.md** — 新功能、新限制、版本号、下载地址都要更新
3. **版本号两处同步**: `Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`
4. **发布流程**: 鹏帅明确要求发新版本后，bump 版本 → git commit + tag → `bash release_all.sh`（Mac DMG + Windows EXE/ZIP 一键构建上传）
5. **GitCode token**: `ydMwBZbLaiex8hRqi-2cma3k`
6. **GitCode 不支持删除 release 附件**, 每次发版用新 tag
7. **发版前必须验证基本功能** (发版检查清单):
   - 启动 app, 确认主面板 token 数不为 0 (后端正常)
   - 确认端口 15723 上 server.py 正常监听
   - 打开热力图弹窗, 确认有数据渲染
   - 打开会话详情, 确认有数据且分页正常
   - 社区功能变更时, 验证 `/api/community/report` 真实成功并能从 `community-data` 分支读回报告
   - 确认 `/api/community` 能区分等待同步、完整排名、Top 10 和读取失败
   - 前端 JS 无语法错误 (`node -e` 校验)
8. **单实例锁健壮性**: 锁文件允许残留，进程存活状态只以内核独占锁为准；成功加锁后才截断并写 PID，避免 Windows 不支持 Unix `Signal(0)` 时误删活锁或产生竞态
9. **只有鹏帅明确说“发新版本”才发布**: 普通修复不改版本号、不打 tag、不推 Release；多项修改可以保留在当前版本号下，等鹏帅确认后统一发版
10. **发版前验证不能省略**:
   - `python3 -m unittest discover -s tests -p 'test_*.py' -v`
   - `cd go_build && go test ./...`
   - 前端内联 JS 语法检查，并确认 `index.html` 与 `go_build/static/index.html` 完全同步
   - Windows `GOOS=windows GOARCH=amd64` GUI EXE 完整编译，确认产物为 PE32+ GUI；检查 ZIP 只含 EXE 和说明
   - macOS 运行 `build_macos.sh`，确认 Swift 编译、图标、签名和版本读取成功
   - 本地 API 冒烟：今日 Token 非 0、热力图日期数量正确、会话分页有数据、check-update 选中正确平台资产
   - Playwright 实际验证 About 更新状态/进度/错误，至少覆盖桌面和移动端
   - 涉及 Windows 自启或自替换时，发布前在真实 Windows 机器做一次登录自启、关闭窗口驻留、更新替换重启验收
   - 自动化部分统一执行 `bash verify_release.sh`；`release_all.sh` 必须在任何 tag/Release 操作前调用它，验证失败立即中止

## 架构

- **macOS**: Swift 壳 (app_wrapper.swift) + Python 后端 (scanner.py / server.py) + HTML 前端 (index.html / chart.js)
- **Windows**: Go 单体 (go_build/main.go), 嵌入前端, 系统托盘, 交叉编译 `GOOS=windows GOARCH=amd64`
- **两版功能完全对齐**: 四源扫描 + 去重 + 模型归一化 + DeepSeek 余额 + 社区排行 + check-update
- **前端同一份** index.html + chart.js, go_build/static/ 是同步副本

## 数据源

- cc-switch: `~/.cc-switch/cc-switch.db` (SQLite)
- Antigravity: `~/Library/Application Support/BingchaAI/usage_stats.json` (macOS 专属, Windows 跳过)
- Hermes: `~/.hermes/state.db` (SQLite)
- WorkBuddy: `~/.workbuddy/workbuddy.db` (SQLite, v1.4.14 接入)

## Windows 限制 (README 中需维护)

- WebView2 内嵌窗口 + 系统托盘，不打开外部浏览器
- 应用内更新直接下载 Release 的 `TokenMonitor.exe`；ZIP 仅供首次/手动安装
- Antigravity 数据源不存在
- 无代码签名 (SmartScreen 拦截)
- 开机自启使用 HKCU Run + `--autostart`，启动后只驻留托盘
- 社区公开排行可读取, 但提交匿名统计要求本机安装 Git 并配置 GitCode 凭据

## 已废弃 (不要恢复)

- start_windows.py / token_monitor.spec (PyInstaller 方案, 放弃)
- release_dmg.sh (被 release_all.sh 替代)
- windows_build/ 目录 (旧尝试, 已删)
- draw_icon.py / icon.png (图标由 build_macos.sh 用 Pillow 动态生成)

## 关键文件

| 文件 | 作用 |
|---|---|
| app_wrapper.swift | macOS Swift 壳 |
| scanner.py | macOS 数据采集 |
| server.py | macOS HTTP 服务 |
| index.html / chart.js | 前端大屏 |
| go_build/main.go | Windows Go 版主程序 |
| release_all.sh | 一键发布 Mac + Windows |
| build_macos.sh | macOS .app 构建 |
| build_dmg.sh | macOS DMG 打包 |
| build_windows.sh | Windows EXE 构建 (Go 交叉编译) |
| docs/PROJECT_STATUS.md | 详细项目状态 |

## Release Notes 规范

- 每次发版时, release notes 要简短说明本次改动, 一两句话即可
- 格式: 中文, 直接写在 git commit message 里 (release_all.sh 会用 tag 对应的 commit message 作为 release body)
- 示例: `feat: 热力图点击下钻 + 会话详情对话浏览; bump v1.3.65`
- **GitCode 不支持更新已有 release 的 body** (PATCH/PUT 返回 405), 所以 release notes 只在创建时写入, 发版后无法修改
- release_all.sh 已改为: 创建 release 时自动取 `git log -1 --format=%s $TAG` 作为 body

## 功能演进历史

### v1.4.19 (2026-07-11)
- Windows 开机自启收敛为 HKCU Run 单入口，补强 LockFileEx 单实例锁，登录后静默驻留托盘
- Windows 应用内更新改为直接下载并校验 EXE，Release 同时提供 EXE 与 ZIP；更新 UI 统一进入 About
- 移除重复的“我的匿名 ID”按钮，新增 `verify_release.sh` 并在发布前强制执行

### v1.3.66 (2026-06-25)
- 修复 Python 版 server.py 漏注册 /api/heatmap 路由导致热力图 404
- 会话详情支持分页: 10/20/50/100 条每页可切换, 上一页/下一页按钮
- Go 版同步实现分页 (SessionDetailResponse 增加 total/page/page_size/total_pages 字段)

### v1.3.65 (2026-06-25)
- 热力图单元格可点击, 弹出该时段 API 调用详情列表
- 会话列表行可点击, 弹出完整对话内容 (用户/助手消息, 按角色着色)
- 新增 /api/session_detail 和 /api/heatmap_detail 接口, Mac/Win 双端对齐

### v1.3.63-1.3.64 (2026-06-24)
- 新增活动热力图 (星期 x 小时, 颜色深浅表示活跃度)
- 新增会话详情列表 (最近 API 调用会话, 含模型/Token/时间戳)
- 修复按钮重复导致的定位错误和点击失效

### v1.3.62 (2026-06-24)
- 周统计/月统计增加区间总消耗和日均消耗

### v1.3.60-1.3.61
- 修复自动更新后应用无法启动
- 修复关于页点立即更新提示暂无可用更新

## 已知问题 & 待办

- cc-switch session_id (UUID) 与 Codex rollout 文件名 (UUIDv7) 不一致, 当前用 timestamp 近似匹配 (600 秒窗口)
- GitCode API 返回 asset size 为 0 (已知行为, 实际文件可正常下载)
- GitCode 不支持删除 release 附件, 如需重新上传必须用新 tag
- SQLite 缓存优化已评估, 当前数据量下直接查源库足够快 (10-100ms), 暂不需要

## 技术决策记录

- **不用 SQLite 缓存层**: 数据源本身已是 SQLite/JSON, 直接查源库 10-100ms, 加缓存层增加同步复杂度但收益微小 (2026-06-25 评估)
- **Go 版用 modernc.org/sqlite**: 纯 Go 驱动, 无 CGO, 支持交叉编译
- **会话详情 max_messages=500**: 防止超大 rollout 文件导致内存爆炸, 分页在前端做
- **Windows 自启只保留一个入口**: HKCU Run 是唯一入口；启用/迁移时清理旧 Startup 快捷方式、计划任务和错误的 StartupApproved 值，避免重复启动
- **Windows 应用内更新不用 ZIP**: Release 同时发布 `TokenMonitor.exe` 与手动安装 ZIP；应用内更新只下载、校验、替换 EXE，并删除可能干扰版本判断的旧 `version.txt`
- **更新 UI 单一入口**: 托盘和原生菜单的更新操作都打开 About 页；后台检查只刷新版本标记，下载进度和错误都在 About 内展示，不使用独立更新弹窗
- **社区入口保持唯一**: 匿名社区 ID、同步状态和排名统一在社区 Dashboard 展示，不恢复首页独立“我的匿名 ID”按钮或重复弹窗

### v1.3.67 (2026-06-25)
- 会话列表、热力图下钻、对话内容三个弹窗统一加分页
- 分页数量可定制: 列表 20/50/100/200 条每页, 对话 10/20/50/100 条每页
- 热力图标题显示实际日期范围 (如 "5月27日 - 6月25日")
- Go 版同步实现分页 (SessionListResponse 增加 page/page_size/total_pages 字段)

### v1.3.68 (2026-06-25)
- 热力图格子悬停 tooltip 显示具体日期和各日消耗明细
- 后端新增 dates 字段: 每个格子返回 [{date, tokens}] 列表
- 超过8天显示前8天 + "...等X天"摘要
- Go 版同步实现

### v1.3.69 (2026-06-25)
- 热力图完全重构为 GitHub 贡献图风格: 横轴按天排列, 每格代表一天
- 顶部标月份, 左侧标星期, 悬停显示具体日期+星期+消耗量
- 点击格子下钻该天调用详情 (heatmap_detail 新增 date 参数)
- 后端 get_heatmap_data 改为返回按天列表, Python/Go 双端同步

### v1.3.70 (2026-06-25)
- 热力图从 30 天扩展至 90 天，从今天往前数 90 天，无数据天显示空白
- 格子缩小至 14px 以适配更多列

### v1.3.71 (2026-06-26)
- 修复: 单实例锁文件残留导致更新后 server.py 无法启动, 主面板显示 0
- server.py / go_build/main.go: 获取锁前检查旧 PID 是否存活, 若已死自动清理
- 新增发版前验证检查清单 (写入核心约定第 7 条)

## 2026-07-10 社区功能复盘

### v1.4.12-v1.4.17 已发布变更
- v1.4.12 加入匿名社区 ID、自动上报、社区聚合和 Dashboard
- v1.4.13 修复 `community.py` 未打进 macOS App Resources 导致后端启动失败
- v1.4.14 接入 WorkBuddy 数据源并加入社区统计；v1.4.15 补发热更新占位版
- v1.4.16 增加顶部匿名 ID 入口；v1.4.17 修复亮色主题背景渐变

### 社区排行未上榜根因 (v1.4.18 已修复)
- GitCode Contents API 创建文件应使用 POST, 更新文件才使用 PUT + sha
- 旧实现新建/更新都使用 PUT: 空 sha 返回 `400 input value is null`, 省略 sha 返回 `400 param is missing`
- `/api/community/report` 忽略 `report_community_stats()` 的 False, 无论失败都返回 `ok: true`
- 因此 `community/reports` 只有 `.gitkeep`, 用户虽显示“已加入”但从未进入数据集, 所有社区数字均为 0
- Windows 另有类型错误: `summary.total_tokens` 实际为 int64, 旧代码只接收 float64, 即使上报成功也会写 0

### 社区数据架构 (2026-07-10)
- 报告写入独立 `community-data` 分支的 `community/reports/User_XXXXX.json`, 不再污染 main 代码历史
- 公开仓库报告无需 token 即可读取；写入仍依赖本机 GitCode 凭据, 这是当前明确限制
- 新文件用 POST, 已有文件用 PUT + sha；成功后立即清除 5 分钟聚合缓存
- 报告增加 `report_date`, 聚合只统计今天的报告, 防止离线用户昨天的数据混进今天
- 完整排名在所有今日报告中计算, Top 10 只用于榜单展示
- UI 状态必须区分: `pending` / `credential_missing` / `ranked` / `outside_top10` / `load_failed`
- 页面不再展示伪“累计总量”或按全部 Token 粗算的“缓存省钱”

### 社区功能发版门禁
- 必须运行 `python3 -m unittest -v tests.test_community`
- 必须验证 Python/Go 编译、Windows 交叉编译和前端 JS 语法
- 必须通过真实 GitCode 链路验证一次 POST 新建和一次 PUT 更新
- 必须使用桌面及 390px 移动视口检查社区弹窗无溢出、状态可读、同步按钮可用

### v1.4.18 (2026-07-10)
- 修复社区报告从未创建、接口误报成功、Windows 上报总量为 0、Top 10 外误显示“未上榜”等问题
- 社区报告迁移到 `community-data` 分支，页面改为清晰的同步状态、完整个人排名和“立即同步”操作
- 发布前通过 Python 5 项单测、Go 单测、Windows 交叉编译、真实 GitCode POST/PUT、桌面及移动端浏览器验证
