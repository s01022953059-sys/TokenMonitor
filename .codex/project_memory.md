# Token Monitor 项目记忆

## 核心约定

1. **称呼用户为"鹏帅"**
2. **每次更新代码后同步刷新 README.md** — 新功能、新限制、版本号、下载地址都要更新
3. **版本号两处同步**: `Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`
4. **发布流程**: bump 版本 → git commit + tag → `bash release_all.sh`（Mac DMG + Windows ZIP 一键构建上传）
5. **GitCode token**: `ydMwBZbLaiex8hRqi-2cma3k`
6. **GitCode 不支持删除 release 附件**, 每次发版用新 tag

## 架构

- **macOS**: Swift 壳 (app_wrapper.swift) + Python 后端 (scanner.py / server.py) + HTML 前端 (index.html / chart.js)
- **Windows**: Go 单体 (go_build/main.go), 嵌入前端, 系统托盘, 交叉编译 `GOOS=windows GOARCH=amd64`
- **两版功能完全对齐**: 三源扫描 + 去重 + 模型归一化 + DeepSeek 余额 + check-update
- **前端同一份** index.html + chart.js, go_build/static/ 是同步副本

## 数据源

- cc-switch: `~/.cc-switch/cc-switch.db` (SQLite)
- Antigravity: `~/Library/Application Support/BingchaAI/usage_stats.json` (macOS 专属, Windows 跳过)
- Hermes: `~/.hermes/state.db` (SQLite)

## Windows 限制 (README 中需维护)

- 无原生窗口 (系统托盘 + 浏览器)
- 无应用内自更新
- Antigravity 数据源不存在
- 无代码签名 (SmartScreen 拦截)
- check-update 返回 .dmg 优先

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
- 格式: 中文, 直接写在 git commit message 里 (release_all.sh 会用 tag 对应的 commit message)
- 示例: `feat: 热力图点击下钻 + 会话详情对话浏览; bump v1.3.65`

## 功能演进历史

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
