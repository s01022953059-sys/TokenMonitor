# Token Monitor 项目状态 (2026-07-10)

> 本文档的主体保留 2026-06-23 的历史说明；以下补充记录 2026-07-10 的最新状态，供后续接手的人快速了解现状。

## 2026-07-10 最新补充

- 当前已发布版本: v1.4.24
- 数据源已扩展为五类: Codex 官方日志 / cc-switch / Antigravity / Hermes / WorkBuddy；Codex 不再依赖 cc-switch，WorkBuddy 改读项目 JSONL 的逐请求 usage，Antigravity 汇总文件仅识别不重复累加
- 新增社区用量排行: 匿名 ID、今日聚合、完整个人排名、Top 10、工具占比、手动/每小时上报
- 社区报告使用独立 `community-data` 分支, 避免自动上报污染 `main` 提交历史
- 2026-07-10 已定位社区全为 0 / 用户未上榜根因: 新建报告误用 PUT, GitCode 要求 POST 创建、PUT + sha 更新
- v1.4.18 还包括: 上报接口返回真实状态、Windows int64 总量写 0 修复、只聚合当日报告、排名不再局限 Top 10、社区页面文案简化
- 社区公开数据无需凭据即可读取；匿名上报改经 `new.taqi.cc` VPS 中继，客户端不再要求安装 Git 或配置 GitCode 凭据
- VPS 仅在本机 `127.0.0.1:18190` 运行 Go 中继，由 Nginx 暴露固定 HTTPS 路径；设备凭据只存本机，服务端只保存 SHA-256
- 2026-07-12 已部署并实测 VPS 中继：两个隔离身份新建/更新成功，错误凭据返回 403，GitCode 报告读回正确，测试报告已清理
- v1.4.22 发布社区昵称：昵称与匿名 ID 分离，SQLite 原子重名检查，7 天冷却、30 天旧名保护及风险名称防护
- 社区功能发版前必须运行 `tests/test_community.py`，并真实验证一次 POST 新建和 PUT 更新

## 项目简介

Token Monitor 是跨平台本地仪表盘，macOS 使用 Swift + Python，Windows 使用 Go；只读合并 Codex、cc-switch、Hermes、WorkBuddy 等本地事件，汇总每日 Token 使用量并提供可视化、历史趋势和应用内更新。

## 当前版本

- **main 分支**: commit `ed02512`
- **latest release**: v1.3.44 (commit `b21567b`)
- **用户 Mac 上装的 .app**: v1.3.44 (装在 `~/Applications/Token Monitor.app`, silent update 路径)

## 已完成的功能

### 数据层 (scanner.py / server.py)
- 多源数据采集: Codex rollout/SQLite、cc-switch、Hermes state.db、WorkBuddy projects JSONL；Antigravity 汇总文件仅识别不重复累加
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
- About 弹窗是唯一更新界面：菜单栏、首页徽章和 About 按钮均进入此处；进度条置顶，下方仅保留立即更新与稍后
- About 更新状态采用短句，当前版本不在状态栏重复；详细错误放悬停提示，并在重新检查、失败和更新过程中正确清理旧按钮、颜色与进度
- macOS `file://` WebView 通过每次启动生成的临时本地凭据写入社区昵称；后端仅对凭据匹配的 `Origin: null` 放行，远程来源仍拒绝
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

- **用户主导版本发布**: 只有鹏帅明确说“发新版本”才允许 bump、tag 和创建 release；普通修复只提交代码和验证结果
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

## Windows 发布: Go 交叉编译 (v1.3.45+, 2026-06-24 定稿)

### 方案

Mac 版用 Python (scanner.py + server.py) + Swift 壳 + HTML 前端。
Windows 版用 **Go 交叉编译**, 在 Mac 上直接产出 Windows EXE, 不需要 Windows 机器 / Wine / Docker。
`modernc.org/sqlite` 是纯 Go, 无 CGO 依赖, 交叉编译零障碍。

之前尝试过 PyInstaller (Docker 镜像源全挂 + Wine 无法在 ARM Mac 上跑 x86 程序), 放弃。
Go 版 `go_build/main.go` 完全重写, 功能与 Python 版 v1.3.45 完全对齐。

### Go 版对齐 Python 版的验证清单

| 功能 | Python 版 | Go 版 | 状态 |
|---|---|---|---|
| 多源扫描 | Codex / cc-switch / Hermes / WorkBuddy | 同 | 对齐 |
| 跨源去重 | `_dedup_events()` 2s 窗口 + 同模型 + 同 token 量 | `dedupEvents()` 同逻辑 | 对齐 |
| 模型归一化 | `normalize_model_name()` 去日期后缀 + qwen3.6-plus 折叠 | `normalizeModelName()` 同逻辑 | 对齐 |
| DeepSeek 余额 | 语义匹配 provider_type/name/app_type LIKE '%deepseek%' | 同 | 对齐 |
| `/api/usage` summary | 12 个字段含 events_dedup | 同 12 个字段 | 对齐 |
| `/api/history` | 动态工具与模型列表，和首页/热力图共用事件口径 | 同 | 对齐 |
| `/api/app-info` | 每次请求重读版本号 | 同 (readAppVersion 每次调用) | 对齐 |
| `/api/check-update` | 用命令行传入的 feed URL, 空值检查, 每次重读版本 | 同 | 对齐 |
| 版本号来源 | Info.plist (多路径回退) | 编译进 EXE 的版本号优先，旧 version.txt 仅兼容开发模式 | 对齐 |
| 单实例锁 | fcntl.flock (Unix) / msvcrt (Windows) | syscall.Flock (Unix) / LockFileEx (Windows) | 对齐 |
| `estimated_cost_usd` | 已删除 | 不存在 | 对齐 |

### Go 版修复记录 (v1.3.45)
1. `checkUpdateRemote` 改用 `feedURL` (命令行参数) 而非硬编码 `updateFeedURL`
2. 空 feed URL 时返回 "未配置更新源" 错误 (对齐 Python 版)
3. `/api/app-info` 和 `/api/check-update` 每次请求重读版本号 (对齐 Python 版 `_read_app_version()`)

### 构建文件

| 文件 | 作用 |
|---|---|
| `build_macos.sh` | 编译 Swift + 拼装 .app + Pillow icns + codesign |
| `build_dmg.sh` | hdiutil 打 DMG |
| `build_windows.sh` | Go 交叉编译 `GOOS=windows GOARCH=amd64`，产出直装 EXE + 手动安装 ZIP |
| `release_all.sh` | **一键发布**: 清空 build/ → Mac DMG + Windows EXE/ZIP → 上传 GitCode Release → 清空 build/ |
| `go_build/main.go` | Go 版主程序 (1121 行, 对齐 Python v1.3.45) |
| `go_build/lock_unix.go` | Unix 单实例锁 (syscall.Flock) |
| `go_build/lock_windows.go` | Windows 单实例锁 (`LockFileEx`) |
| `go_build/static/` | 嵌入的 index.html + chart.js (与根目录同步) |

### 发布流程

```bash
# 1. bump 版本号 (两处)
#    Info.plist: <string>1.3.45</string>
#    go_build/main.go: var appVersion = "1.3.45"

# 2. git 提交 + 打 tag
git add -A
git commit -m "bump: v1.3.45"
git tag v1.3.45
git push origin main
git push origin v1.3.45

# 3. 一键发布 (Mac DMG + Windows EXE/ZIP 同时构建上传)
bash release_all.sh
```

`release_all.sh` 做的事:
1. 清空 `build/` 目录 (防止旧产物混入)
2. `build_macos.sh` 编译 .app → `build_dmg.sh` 打 DMG → 上传 "Token Monitor.dmg"
3. `build_windows.sh` Go 交叉编译 EXE → 上传 `TokenMonitor.exe`，并打 ZIP 上传 `TokenMonitor-win.zip`
4. 清空 `build/` 目录
5. 验证 Release 附件列表

### GitCode Release API (上传附件)

GitCode 不支持删除 release 附件, 所以每次发新版本用新 tag。
上传是两步流程:
1. `GET /releases/:tag/upload_url?file_name=xxx` → 拿到预签名 PUT 地址 + headers
2. `PUT` 文件到该地址

凭据从 `git credential fill` (host=gitcode.com) 读取, 不硬编码 token。

### 下载地址 (v1.3.45)

- Mac: `https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.45/Token Monitor.dmg`
- Windows: `https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.45/TokenMonitor-win.zip`

### Windows 数据源路径

- cc-switch: `%USERPROFILE%\.cc-switch\cc-switch.db` (Go `os.UserHomeDir()` 自动适配)
- Antigravity: macOS 专属路径, Windows 上自动跳过 (文件不存在)
- Hermes: `%USERPROFILE%\.hermes\state.db`

### 已废弃的文件

| 文件 | 状态 |
|---|---|
| `start_windows.py` | 废弃 (PyInstaller 方案, 不再使用) |
| `token_monitor.spec` | 废弃 (PyInstaller 方案, 不再使用) |

## 已知问题与修复记录 (2026-06-24)

### 问题 1: codex_session 源 model 名称错误 (已修复, 待发布)

**现象**: 用户选择了 Zhipu GLM provider, 但 dashboard 模型分布饼图出现大量 `gpt-5.5` token。

**根因**: cc-switch 的 `codex_session` 数据源在同步 `~/.codex/sessions/` 日志时, 把 session 文件中的 `request_model` 字段 (客户端声明的模型, 如 `gpt-5.5`) 直接当作实际使用的 model, 而没有用 session 文件里 `turn_context` 中的真实 model (如 `glm-5.2`)。

**证据**:
- session 文件 `~/.codex/sessions/2026/06/24/rollout-...019ef77e...jsonl`:
  - `session_meta`: `model_provider=custom`, `originator=Codex Desktop`
  - `turn_context`: `model=glm-5.2`
- cc-switch DB `proxy_request_logs` 表同一 session (`019ed333`):
  - `data_source='codex_session'`: 800 条记录, model=`gpt-5.5`, provider_id=`_codex_session`
  - `data_source='proxy'`: 0 条记录 (说明该 session 的请求未经代理实时记录)
- 当前活跃 provider: Zhipu GLM (`is_current=1`)

**修复**: `scanner.py` 的 `_load_provider_model_map()` 重写, 返回 `active_model_by_app` (app_type → 当前活跃 provider 的实际 model)。`scan_cc_switch_logs()` 中对 `data_source='codex_session'` 的记录, 用当前活跃 Codex provider 的实际 model 替换 `gpt-5.5` → `glm-5.2`。

### 问题 2: 跨数据源重复计算 (已修复, 待发布)

**现象**: 同一笔请求同时出现在 `proxy` (实时代理记录) 和 `codex_session` (日志同步) 两个数据源中, 导致 token 被计算两次, 每天多计约 5470 万 tokens。

**根因**: 旧去重逻辑要求 `(time_bucket, model, total_tokens)` 完全匹配, 但同一请求在两个源中记录的 model 名称不同 (proxy 记 `glm-5.2`, codex_session 记 `gpt-5.5`), 导致去重失败。

**修复**: `get_historical_usage()` 中 `get_historical_usage()` 增加行级去重, dedup key 从 `(time_bucket, model, total_tokens)` 改为 `(time_bucket, total_tokens)`, 在 model 修正之前先按时间和 token 量去重。

### 问题 3: 图片处理的模型归属 (已分析, 代码层面无需额外修复)

**用户疑问**: GLM-5.2 不支持图片识别, 但 Codex 能读懂截图。图片发给了哪个模型? 为什么没统计?

**分析结论**:
- Codex Desktop 把用户截图作为 `input_image` 类型内容, 放在 user message 里, 和文字一起发给当前配置的模型 (glm-5.2)
- 没有单独的 vision 模型通道, 图片 token 和文字 token 合并在同一次请求中
- session 文件确认: 包含 `input_image` 的 message, 对应的 `turn_context` model 是 `glm-5.2`
- 图片 token 之所以"没统计到", 是因为 `codex_session` 源把这些请求的 model 错标为 `gpt-5.5` (问题 1), 修复后会被正确归到 `glm-5.2`
- GLM-5.2 是否真正处理了图片内容 (还是忽略图片只处理文字), 这取决于 GLM-5.2 API 本身的行为, 超出 Token Monitor 的可控范围

### 版本说明

- 以上三个问题的修复已 commit + push 到 GitCode main 分支
- **版本号未 bump** (用户要求统一修改版本号)
- 用户需通过自动更新拉取新版本后验证修复效果
