# Token Monitor 项目状态 (2026-06-23)

> 本文档记录 Token Monitor 项目截至 2026-06-23 的完整进展, 供后续接手的人 (或其他 Agent) 快速了解现状。

## 项目简介

Token Monitor 是 macOS 本地仪表盘, 跨 Swift (UI 壳) + Python (scanner/server) + HTML/CSS/JS (前端), 只读扫描 cc-switch / Antigravity (冰茶) / Hermes 三源数据, 汇总每日 token 使用量, 提供大屏可视化 + 历史趋势 + 应用内自更新。

## 当前版本

- **main 分支**: commit `ed02512`
- **latest release**: v1.3.44 (commit `b21567b`)
- **用户 Mac 上装的 .app**: v1.3.44 (装在 `~/Applications/Token Monitor.app`, silent update 路径)

## 已完成的功能

### 数据层 (scanner.py / server.py)
- 三源数据采集: cc-switch (`~/.cc-switch/cc-switch.db`) / Antigravity (`~/Library/Application Support/BingchaAI/usage_stats.json`) / Hermes (`~/.hermes/state.db`)
- 三源去重: 按 `timestamp ± 2s + 同模型 + 同 token 量` 近似匹配, 避免同一笔请求被多源重复计入
- DeepSeek provider 语义查询: 从硬编码 `id='ddsds'` 改成按 `provider_type / name / app_type LIKE '%deepseek%'` 匹配
- 成本估算**删除** (用户明确不需要, 只关注 token 量)
- `normalize_model_name()` 折叠 cc-switch 噪声变体: `qwen3.6-Plus` (大写 P) / `qwen3.6-plus-2026-04-02` (带日期) → `qwen3.6-plus`
- APP_VERSION 每次请求重读 Info.plist (修 About 弹窗显示过期版本 bug, 因为自更新后旧 server.py 进程仍跑)
- APP_VERSION 多路径支持: `<source>/Info.plist` / `/Applications/.../Info.plist` / `~/Applications/.../Info.plist`

### UI 层 (index.html)
- 深色主题 + 亮色主题切换
- 双圆环图 (donut): 左边按工具, 右边按模型
- **按排名固定色盘** `RANK_COLORS` (暖→冷): top1 红 → top2 橙 → top3 黄 → top4 绿 → top5 青 → top6 蓝 → top7 紫 → top8 粉 → top9+ 灰
  - 左右两个 donut 用同一套, top1 永远红 (燃烧), top2 永远橙
- **内圈柔和化**: 去掉硬 border, 改 `inset box-shadow` 柔和光晕, radial-gradient 边缘柔过渡
- **总量级别灯** (内圈背景按用量变色): <20M 蓝 / 20-100M 绿 / 100-300M 黄 / >300M 红
- 历史趋势弹窗 (Chart.js, 7/14/30 天, 工具/模型维度)
- About 弹窗 (4 个更新入口: NSAlert / 状态栏菜单 / 首页徽章 / About 按钮)
- 首页版本徽章: 有新版本时显示橙色脉冲红点, 点击打开 About

### 应用内自更新 (app_wrapper.swift + update_helper.sh)
- **下载**: URLSession + cache buster query (`?_tm=<timestamp>`) 避免 NSURLCache 命中 gitcode download-error 占位
- **CDN 占位 retry**: content-length < 10KB 视为占位, retry 1 次 (同 IP 拿同占位时仍可能失败, 用户多点几次)
- **解压**: `/usr/bin/ditto -x -k` (避开 macOS unzip 对中文文件名 `启动 Token Monitor.bat` 的 bug)
- **编译**: `build_macos.sh` (swiftc + 拼装 .app + ad-hoc 签名 + Pillow 生成 AppIcon.icns)
- **替换**: helper 用 `ditto` 替换 .app, `~/Applications/` 路径 silent (无密码), `/Applications/` 路径 osascript sudo 弹窗
- **helper 自举**: `performAppReplacement` 优先用 stagedApp (新版本) 里的 helper, 不用当前 app (旧版本) 的
- **真重启修复**: helper 替换 .app 后先 `pkill -f <bundle id>` 杀老进程, sleep 1s, 再 `open -b` 拉新 (commit `450441b`, 修老进程不退导致大屏显示旧版本)
- **webView 竞态修复**: `applicationDidFinishLaunching` 延迟 1.5s 再 `checkForUpdates`, 给 webView 时间注册 `__tokenMonitorOnUpdateAvailable` JS callback
- **NSAlert 简化**: 只留 "立即更新" + "稍后" 两个按钮 (移除 "下载 zip" / "查看说明")

### 构建与发布
- `build_macos.sh`: swiftc 编译 + 拼装 .app/Contents 结构 + Pillow 转 AppIcon.icns + codesign ad-hoc
- `build_dmg.sh`: hdiutil 打 dmg (UDZO + HFS+, 含 .app + /Applications symlink, 双击拖装)
- `install.sh`: 一站式安装脚本, 自动选 system/user 模式 (`--user` flag 强制 `~/Applications/`)
- `release_dmg.sh`: 一键 build + 通过 GitCode Release API 上传 DMG 附件 (commit `5c89ea9`)
- 代码层去冗余: 删 `extract_zip.py`, `icon.png` 不再进 Resources (节省 ~160 KB)

## git 状态

- **main**: `5c89ea9` (release_dmg.sh 改用 API 上传)
- **latest release tag**: v1.3.44 (`b21567b`)
- **release tag 列表**: v1.1, v1.2, v1.3, v1.3.1 ~ v1.3.44 (共 44+ 个)
- **空 release commit** (仅 bump 版本号, 验证自更新用): v1.3.7, v1.3.8, v1.3.9, v1.3.18, v1.3.20, v1.3.22, v1.3.26, v1.3.29, v1.3.30 等 — **不删**, 是 git history 一部分

## 用户 Mac 上的状态

- `~/Applications/Token Monitor.app` = v1.3.44 (silent update 路径, 无密码升级)
- `/Applications/` 无 Token Monitor (已删, 之前老版本)
- `~/Downloads/tm-update/` 有 v1.3.44 解压目录 (测试用)
- cc-switch.db 100 MB, 包含 codex / claude / claude-desktop 等 app 的请求日志
- codex 实际走 bailian provider 调 qwen3.6-plus (用户 cc-switch UI 选 MiniMax 但 codex 进程没跟着切, 继续用 bailian)

## 未完成 / 待决定

### dmg 发布 (已解决)
- **GitCode API 支持上传 Release 附件**: 两步流程 — 先 `GET /releases/:tag/upload_url?file_name=xxx` 拿预签名 PUT 地址, 再 `PUT` DMG 到该地址
- `release_dmg.sh` (commit `5c89ea9`) 自动完成编译 + 打 DMG + API 上传, 凭据从 `git credential` 读取
- v1.3.44 的 DMG 已上传, 下载地址: `https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.44/Token Monitor.dmg`
- 自动更新链路: `server.py` 的 `/api/check-update` 从 Release `assets` 数组里挑 `.dmg` 附件的 `browser_download_url`, Swift 端 `parseUpdateInfo` 同样从 `assets` 取下载地址

### 其他待办
- UI 文案简化 (用户提过 "下载更新包" → "下载中" 等, **没**改)
- SMAppService daemon 注册 (完全 silent update, 需要 macOS engineer + Apple Developer Account, 沙箱做不了)
- git 空 commit 历史清理 (需要 rebase, 破坏 git history, **不建议**)
- `go_build/` `windows_build/` 目录清理 (Windows 端, 用户不用但**没**删)
- CDN 占位 retry 仍不稳定 (同 IP retry 拿同占位, 用户多点几次才行; 根因是 gitcode CDN 对某些 IP 路由到 download-error 占位)

## 协作约定

- **用户主导版本发布**: 我改完代码等用户确认版本号后才发 release, 不主动 bump 版本号 (见 memory `collaboration_mode`)
- **沙箱限制**: Linux 沙箱没 `swiftc` / `hdiutil` / `ditto` / `osascript`, 无法真正编译 / 打 dmg / 验证 macOS 行为, 只能写脚本 + 语法检查 + mock 验证逻辑
- **自更新路径已验证**: v1.3.10 → v1.3.44 多次走通, 不要重做架构

## 关键文件

| 文件 | 作用 |
|---|---|
| `app_wrapper.swift` | Swift UI 壳, WKWebView + 状态栏 + 自更新逻辑 |
| `scanner.py` | Python 数据采集, 三源扫描 + 去重 + 归一化 |
| `server.py` | Python HTTP 服务, 4 个 API + 单实例锁 |
| `index.html` | 前端大屏, 双 donut + 历史趋势 + About 弹窗 |
| `start.sh` | 启动脚本, 单实例闸门 + nohup 拉 server.py |
| `update_helper.sh` | 自更新 helper, 替换 .app + kill 老进程 + 重启 |
| `build_macos.sh` | 编译 .app (swiftc + Pillow icns + codesign) |
| `build_dmg.sh` | 打 dmg (hdiutil) |
| `install.sh` | 一站式安装 (system/user 模式) |
| `release_dmg.sh` | 一键 build + GitCode API 上传 DMG 到 Release 附件 |
| `Info.plist` | 版本号 + 端口 + 更新源 URL |

## Windows 发布 (2026-06-24 新增)

### 跨平台改造
- `server.py` 的单实例锁从 `fcntl` (Unix 专属) 改为跨平台: Unix 用 `fcntl.flock`, Windows 用 `msvcrt.locking`
- `SINGLETON_LOCK_PATH` 从硬编码 `/tmp/` 改为 `tempfile.gettempdir()`, 兼容 Windows `%TEMP%`

### Windows 构建文件
| 文件 | 作用 |
|---|---|
| `start_windows.py` | Windows 启动器 (替代 start.sh), 单实例检查 + 后台启动 server.py + 自动打开浏览器 |
| `token_monitor.spec` | PyInstaller 打包配置, 把 scanner.py / server.py / index.html / chart.js 打进单个 EXE |
| `build_windows.sh` | Windows 上执行: pip install pyinstaller → PyInstaller 打包 → ZIP |
| `release_all.sh` | 统一发布: Mac DMG + Windows ZIP 同时上传到 GitCode Release |

### Windows 数据源路径
- cc-switch: `%USERPROFILE%\.cc-switch\cc-switch.db` (Python `os.path.expanduser("~")` 自动适配)
- Antigravity: macOS 专属路径, Windows 上自动跳过 (文件不存在)
- Hermes: `~/.hermes/state.db`, Windows 上路径为 `%USERPROFILE%\.hermes\state.db`

### 在 Windows 上构建
```bash
# 需要 Python 3.10+ (从 python.org 下载)
pip install pyinstaller
bash build_windows.sh
# 产出: build/TokenMonitor-<version>-win.zip
```

### 统一发布流程 (Mac + Windows)
```bash
# Mac 上跑 (只发 DMG):
bash release_all.sh

# Windows 上跑 (只发 ZIP):
bash release_all.sh

# 完整发布: Mac 上发 DMG, Windows 上发 ZIP, 都上传到同一个 Release tag
```

## Windows 发布 v2: Go 交叉编译 (2026-06-24 更新)

### 方案变更
- 放弃 PyInstaller (网络环境无法拉 Docker 镜像 / 装 Wine)
- 改用 **Go 交叉编译**: `GOOS=windows GOARCH=amd64 go build`, 在 Mac 上直接产出 Windows EXE
- `modernc.org/sqlite` 是纯 Go, 无 CGO 依赖, 交叉编译零障碍

### Go 版完全对齐 Python 版 v1.3.44
- `go_build/main.go` 完全重写 (1121 行), 移除旧 v1.1 的 Codex 原生日志 / Claude Code JSONL 等多余源
- 三源扫描: cc-switch / Antigravity / Hermes (与 Python 版完全一致)
- 跨源去重: `dedupEvents()` 对齐 `_dedup_events()` (时间窗口 2s + 同模型 + 同 token 量)
- 模型归一化: `normalizeModelName()` 对齐 `normalize_model_name()` (去日期后缀 + qwen3.6-plus 家族折叠)
- DeepSeek 余额: 语义匹配 (provider_type/name/app_type LIKE '%deepseek%'), 不再硬编码 id
- `/api/check-update`: 完整实现, 对齐 server.py 的版本比较 + asset URL 选取
- `/api/app-info`: 动态版本号 (从 version.txt 或 Info.plist 读取)
- 删除 `estimated_cost_usd` (用户明确不需要)
- 单实例锁: 跨平台 (Unix flock / Windows 文件检测)

### 构建文件
| 文件 | 作用 |
|---|---|
| `build_windows.sh` | Go 交叉编译 Windows EXE + 打 ZIP (在 Mac 上直接跑) |
| `release_all.sh` | 一键: Mac DMG + Windows ZIP 同时构建上传到 GitCode Release |
| `go_build/lock_unix.go` | Unix 单实例锁 (fcntl.flock) |
| `go_build/lock_windows.go` | Windows 单实例锁 (文件检测) |

### 验证结果
- Mac 上 Go 版 API 输出与 Python 版对比: by_tool / by_model / recent_events / events_dedup / DeepSeek 余额 全部一致
- Windows EXE: 16MB (PE32+ x86-64), ZIP 4.6MB
- v1.3.44 Release 已上传 TokenMonitor-win.zip
- 下载地址: `https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.44/TokenMonitor-win.zip`

### 统一发布流程
```bash
# 一键发布 Mac + Windows:
bash release_all.sh

# 只发 Windows:
bash build_windows.sh
# 然后手动上传 ZIP 到 GitCode Release
```
