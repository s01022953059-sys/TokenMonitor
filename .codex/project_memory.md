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
