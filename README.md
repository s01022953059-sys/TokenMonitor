# Token Monitor

本地 Token 使用量监控仪表盘。只读扫描多个 AI 工具的数据源，汇总每日 Token 消耗，提供可视化大屏 + 历史趋势 + 应用内自更新。

支持 **macOS** 和 **Windows** 双平台。

## 功能

### 数据采集

只读扫描三个数据源，不修改任何原始数据：

| 数据源 | 路径 | 说明 |
|---|---|---|
| cc-switch | `~/.cc-switch/cc-switch.db` | SQLite，记录所有经过代理的 API 请求 |
| Antigravity (冰茶 AI) | `~/Library/Application Support/BingchaAI/usage_stats.json` | JSON，每日统计 |
| Hermes | `~/.hermes/state.db` | SQLite，会话级记录 |

三源数据合并后做**跨源去重**：同一时间窗口（2 秒）内 + 同模型 + 同 Token 量视为同一事件，只计一次。避免同一笔请求被多个数据源重复计入。

**模型名归一化**：自动折叠 cc-switch 写入的噪声变体（如 `qwen3.6-Plus` / `qwen3.6-plus-2026-04-02` 统一为 `qwen3.6-plus`）。

**DeepSeek 余额查询**：从 cc-switch 数据库中按语义匹配（provider_type / name / app_type 含 deepseek）提取 API Key，请求 DeepSeek 官方余额接口，每 60 秒刷新一次。

### 仪表盘

- 双圆环图（donut）：左侧按工具，右侧按模型
- 按排名固定色盘（top1 红 → top2 橙 → top3 黄 → ...）
- 总量级别灯：内圈背景按用量变色（<20M 蓝 / 20-100M 绿 / 100-300M 黄 / >300M 红）
- 历史趋势弹窗：7/14/30 天，工具和模型两个维度
- About 弹窗：版本号 + 更新状态
- 深色 / 亮色主题切换

### 活动热力图

- 按天展示每日 Token 消耗量，颜色深浅 = 消耗量（GitHub 5 档绿色色阶）
- **每格右下角**叠加当天 token 短标签（如 `1.2M` / `234K`），不用悬停也能直观看出数量级
- 顶部 3 个统计卡片：总消耗 / 活跃天数（X/Y + 覆盖率）/ 最高单日（值 + 日期）
- **点击任意单元格**查看该日的完整调用列表（模型、Token、缓存命中、延迟）
- Tab 切换时间窗口：30 / 90 / 180 / 365 天（默认 365 天，跨年看全年趋势）
- 横轴标月份，每格代表一天

### 会话详情浏览

- 会话列表展示最近的 API 调用会话，含模型、Token 消耗、时间戳
- **点击任意会话行**查看完整对话内容（用户消息 + 助手回复）
- 会话列表、热力图下钻、对话内容三个弹窗均支持分页
- 分页数量可定制: 20/50/100/200 条每页 (列表) 或 10/20/50/100 条每页 (对话)
- 每个弹窗底部显示总条数和当前页码
- 对话内容从 Codex rollout JSONL 文件中提取，按角色着色区分

### 应用内自更新（仅 macOS）

macOS 版通过 Swift 壳实现完整的应用内自更新：
- 从 GitCode Release API 检查新版本
- 下载 DMG → 解压 → 替换 .app → 杀旧进程 → 重启
- `~/Applications/` 路径静默升级（无密码），`/Applications/` 路径需 sudo 弹窗

### API 接口

本地 HTTP 服务（默认端口 15723）提供 6 个 API：

| 接口 | 说明 |
|---|---|
| `GET /api/usage` | 今日 Token 汇总（总量/输入/输出/缓存/DeepSeek 余额 + 按工具/模型拆分 + 最近 30 条事件） |
| `GET /api/history` | 过去 30 天每日趋势（按工具/模型拆分） |
| `GET /api/app-info` | 应用信息（名称/版本/更新源） |
| `GET /api/check-update` | 检查更新（请求 GitCode Release API，比较版本号，返回下载地址） |
| `GET /api/session_detail` | 会话详情（按 session_id 或时间戳匹配 Codex rollout 文件，返回对话内容） |
| `GET /api/heatmap_detail` | 热力图详情（按星期 + 小时返回该时段的 API 调用列表） |

## 平台实现

### macOS

| 层 | 技术 | 文件 |
|---|---|---|
| UI 壳 | Swift (WKWebView + 状态栏 + 自更新) | `app_wrapper.swift` |
| 后端 | Python (HTTP server + scanner) | `server.py` / `scanner.py` |
| 前端 | HTML/CSS/JS (Chart.js) | `index.html` / `chart.js` |
| 启动 | Bash 脚本 (单实例锁 + nohup) | `start.sh` |
| 安装 | 一站式安装脚本 | `install.sh` |

macOS 版有完整的原生体验：WKWebView 内嵌网页、状态栏菜单、Dock 图标、NSAlert 更新提示、自动下载安装更新包。

### Windows

| 层 | 技术 | 文件 |
|---|---|---|
| 全部 | Go (HTTP server + scanner) | `go_build/main.go` |
| 前端 | 嵌入的 HTML/CSS/JS (与 macOS 同一份) | `go_build/static/` |
| SQLite | modernc.org/sqlite (纯 Go, 无 CGO) | — |
| 启动 | .bat 脚本 (console 窗口 + 自动开浏览器) | `启动TokenMonitor.bat` |

Windows 版用 Go 交叉编译产出单个 EXE，无需安装 Python 或任何运行时。双击 `启动TokenMonitor.bat` 启动，弹出控制台窗口并自动打开浏览器。

**Go 版与 Python 版功能完全对齐**，包括：三源扫描、跨源去重、模型归一化、DeepSeek 余额语义匹配、check-update 端点、每次请求重读版本号。

## Windows 平台限制

| 限制 | 说明 |
|---|---|
| **首次安全拦截** | macOS 端 Gatekeeper 会拦截未签名 app, Windows 端 SmartScreen 拦截未签名 EXE. 详见下文"绕过安全限制" |
| **无控制台窗口** | v1.3.83+ Win 端用 `TokenMonitorLauncher.exe` 弹独立 WebView2 窗口 (隐藏 cmd 窗口), `TokenMonitor.exe` 仍保留 console 模式 |
| **Win 端自更新** | macOS 自更新 (Swift `performAutoUpdate` 下源码 zip 本地 build) 暂时不能直接用, Win 端 launcher 检测到新版本后弹提示让用户去 GitCode 手动下 |
| **Antigravity 数据源** | Antigravity (冰茶 AI) 的统计数据路径是 macOS 专属的 (`~/Library/Application Support/`)，Windows 上该文件不存在，自动跳过 |
| **单实例锁机制不同** | macOS 用 `fcntl.flock` 文件锁，Windows 用端口占用检测 |
| **check-update 下载地址** | macOS 自更新会下源码 zip 本地 build; Win 端 check-update 返回 DMG url 但用户需手动下 zip |
| **版本号来源** | macOS 从 `Info.plist` 读取，Windows 从同目录 `version.txt` 读取（打包时写入），回退到编译时常量 |
| **无 codesign / notarize** | 个人项目无 Apple 开发者账号 ($99/yr) / Win 代码签名证书 ($200-500/yr), 用户首次需手动绕过 Gatekeeper / SmartScreen |

## 安装

> **首次安装安全提示** — Token Monitor 是个人项目, **没有 Apple 开发者账号 / Windows 代码签名证书**,
> 所以 macOS 端会被 Gatekeeper 拦截, Windows 端会被 SmartScreen 拦截. **这是预期行为, 不是 bug**.
> 按下面的"绕过安全限制"步骤操作即可 (30 秒搞定). 未来计划做苹果公证 + Win 代码签名.

### 绕过安全限制

#### macOS (Gatekeeper)

第一次双击 `Token Monitor.app` 会弹:

```
"Token Monitor" 无法打开, 因为来自身份不明的开发者.
```

**两种方法二选一:**

**方法 A (图形界面, 推荐):**
1. 在 Launchpad / Finder 找到 `Token Monitor.app`
2. **右键点击** → "打开" → 弹窗再次点"打开"
3. 之后双击就能正常打开了

**方法 B (命令行, 干净彻底):**
```bash
# 删除下载时附加的"隔离属性"标记 (跟右键打开效果一样)
xattr -dr com.apple.quarantine "/Applications/Token Monitor.app"

# 如果还是被拦, 试强制 ad-hoc 签名 (避开 Gatekeeper 的"完全未签名"判定)
sudo codesign --force --deep --sign - "/Applications/Token Monitor.app"

# 如果 dock / Launchpad 还是显示占位符 (没识别 T+火焰 icns),
# 强制 LaunchServices 重新索引图标缓存:
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
sudo "$LSREGISTER" -f -R -trusted "/Applications/Token Monitor.app"
# 之后重启 Dock: killall Dock
```

**macOS 15 Sequoia 额外提示**: 系统设置 → 隐私与安全性 → 滚到最下面 → 点"仍要打开"按钮.
后续升级 (v1.3.51 → v1.3.83) 因 app 内有自更新, 装新版本后**同样需要重新做一次右键"打开"** (Gatekeeper 重新检查).
如果 v1.3.83 之前装过老版本 (.accessory 模式, dock 没图标), lsregister 重索引 + killall Dock 即可让 dock 重新出现 T+火焰 图标.

#### Windows (SmartScreen)

第一次双击 `TokenMonitorLauncher.exe` 会弹:

```
Windows 已保护你的电脑
Microsoft Defender SmartScreen 阻止了无法识别的应用启动
```

**绕过步骤:**
1. 点击弹窗里的 **"更多信息"** (左下角小字)
2. 出现 **"仍要运行"** 按钮, 点击
3. 弹"你确定要运行此应用吗" → 点"是"
4. 之后双击就能正常打开了 (SmartScreen 记住你的选择)

**WebView2 运行时**: TokenMonitorLauncher.exe 内嵌 WebView2 显示仪表盘, **要求 Windows 10 1809+ (Build 17763) 或更高**.
大部分 Win10/11 自带 WebView2; 如果是精简版, 下载安装:
<https://developer.microsoft.com/microsoft-edge/webview2/>

### macOS

**方式一: DMG 安装**

```bash
# 下载 DMG
curl -L -o "Token Monitor.dmg" \
  "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.83/Token Monitor.dmg"

# 双击挂载, 拖 Token Monitor.app 到 Applications
open "Token Monitor.dmg"
# 然后做上面"绕过 Gatekeeper"步骤
```

**方式二: 脚本安装**

```bash
git clone https://gitcode.com/baggiopeng/TokenMonitor.git
cd TokenMonitor
bash install.sh          # 装到 /Applications
bash install.sh --user   # 装到 ~/Applications (无需密码, 静默升级)
# 然后做上面"绕过 Gatekeeper"步骤
```

### Windows

```bash
# 下载 ZIP
curl -L -o TokenMonitor-win.zip \
  "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.83/TokenMonitor-win.zip"
```

解压后**双击 `TokenMonitorLauncher.exe`** (不是 `TokenMonitor.exe`):
- `TokenMonitorLauncher.exe` → 弹独立 WebView2 窗口显示仪表盘 (不打开你的浏览器)
- `TokenMonitor.exe` → 弹控制台窗口, 不打开浏览器 (开发者/调试模式)

启动后浏览器访问: http://127.0.0.1:15723
停止服务: 任务管理器结束 `TokenMonitor.exe` 进程
首次运行需要按上面"绕过 SmartScreen"步骤.

无需安装 Python 或任何运行时, 单个 EXE 即可运行.

## 构建

### 前置条件

- macOS 11+ (Apple Silicon 或 Intel)
- Xcode Command Line Tools (`xcode-select --install`)
- Python 3.8+ (macOS 版后端)
- Go 1.21+ (Windows 交叉编译)
- Pillow (`pip install Pillow`, 生成图标)

### 构建 macOS .app + DMG

```bash
bash build_macos.sh    # 编译 Swift → 拼装 .app → 生成 icns → codesign
bash build_dmg.sh      # hdiutil 打 DMG
```

### 构建 Windows EXE

在 macOS 上直接交叉编译，不需要 Windows 机器：

```bash
bash build_windows.sh  # GOOS=windows GOARCH=amd64 go build → 打 ZIP
```

产出：`build/TokenMonitor-<version>-win.zip`

### 一键发布（Mac + Windows）

```bash
# 1. bump 版本号 (两处)
#    Info.plist: <string>1.3.47</string>
#    go_build/main.go: var appVersion = "1.3.47"

# 2. git 提交 + 打 tag
git add -A
git commit -m "bump: v1.3.51"
git tag v1.3.51
git push origin main
git push origin v1.3.51

# 3. 一键发布
bash release_all.sh
```

`release_all.sh` 做的事：
1. 清空 `build/` 目录
2. 构建 Mac .app → DMG → 上传到 GitCode Release
3. Go 交叉编译 Windows EXE → ZIP → 上传到同一 Release
4. 清空 `build/` 目录
5. 验证 Release 附件

## 项目结构

```
.
├── app_wrapper.swift          # macOS Swift 壳 (WKWebView + 状态栏 + 自更新)
├── scanner.py                 # macOS 数据采集 (三源 + 去重 + 归一化)
├── server.py                  # macOS HTTP 服务 (4 API + 单实例锁)
├── index.html                 # 前端大屏 (双 donut + 趋势 + About)
├── chart.js                   # Chart.js v4.5.1
├── start.sh                   # macOS 启动脚本
├── install.sh                 # macOS 安装脚本
├── update_helper.sh           # macOS 自更新 helper
├── build_macos.sh             # macOS .app 构建
├── build_dmg.sh               # macOS DMG 打包
├── build_windows.sh           # Windows EXE 构建 (Go 交叉编译)
├── release_all.sh             # 统一发布脚本 (Mac + Windows)
├── Info.plist                 # 版本号 + 端口 + 更新源 URL
├── go_build/
│   ├── main.go                # Windows Go 版主程序 (HTTP + scanner + 托盘)
│   ├── icon.go                # 嵌入图标
│   ├── icon.ico               # 托盘图标
│   ├── lock_unix.go           # Unix 单实例锁 (syscall.Flock)
│   ├── lock_windows.go        # Windows 单实例锁
│   ├── go.mod                 # Go 模块定义
│   └── static/                # 嵌入的前端文件 (与根目录同步)
│       ├── index.html
│       └── chart.js
└── docs/
    └── PROJECT_STATUS.md      # 详细项目状态文档
```

## 技术说明

### 为什么 Mac 用 Python、Windows 用 Go？

macOS 版最初用 Python 开发，后续迭代了 40+ 个版本，功能稳定。Swift 壳负责 UI 和自更新，Python 负责数据采集和 HTTP 服务，各司其职。

Windows 版需要单文件可执行（不要求用户装 Python），而 PyInstaller 在开发者的 Mac 上无法交叉编译（Docker 镜像源失效 + ARM Mac 无法跑 Wine）。Go 的纯 Go SQLite 驱动 (`modernc.org/sqlite`) 让交叉编译变得简单：`GOOS=windows GOARCH=amd64 go build` 一条命令产出 EXE。

两版的数据采集逻辑完全对齐（三源 + 去重 + 归一化 + DeepSeek 余额），API 返回格式一致，前端同一份 `index.html`。

### GitCode Release 上传

GitCode 的 Release 附件上传是两步流程：
1. `GET /releases/:tag/upload_url?file_name=xxx` → 获取预签名 PUT 地址
2. `PUT` 文件到该地址

凭据从 `git credential fill` (host=gitcode.com) 读取，不硬编码 token。

GitCode 不支持通过 API 删除 release 附件，因此每次发版使用新 tag。

## 下载

最新版本：[v1.3.83](https://gitcode.com/baggiopeng/TokenMonitor/releases/v1.3.83)

- macOS: [Token Monitor.dmg](https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.83/Token%20Monitor.dmg)
- Windows: [TokenMonitor-win.zip](https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.83/TokenMonitor-win.zip)

## 最近更新

### v1.3.83 (2026-06-26)

**优化**
- 首页 "已支持平台" 表下"说明"段细化代理/直连路径
  - 之前只说"代理: Claude, OpenCode" / "直连: Codex, Hermes, AntiGravity"
  - 现在每行带 scanner 主读取路径 (cc-switch.db / 工具原生日志)
  - 加一句 "部分工具 (Codex / Hermes) 走直连+代理两路, scanner 跨源去重合并"
  - 帮用户理解为什么同一个工具在表里出现一次但实际数据来自多条路径

### v1.3.80 (2026-06-26)

**修复**
- 修复模型大小写不一致导致 "minimax-m3" / "MiniMax-M3" 两个独立条目
  - 根因: `scan_hermes_tokens` 直接用 raw model 字符串, 没走 `normalize_model_name`
  - Hermes DB 里历史存了 "MiniMax-M3" (大写), cc-switch 路径都 lowercase, 两路同源不合并
  - 修复: `scan_hermes_tokens` 也调 `normalize_model_name`
  - 同步 go_build/main.go 对应 Hermes scanner
  - 验证: by_model 现在只有 1 个 "minimax-m3" 条目, 数字合并前一致 (5.86M)

### v1.3.79 (2026-06-26)

**优化**
- 首页"已支持平台"表刷新, 跟 v1.3.78 工具归一化对齐
  * 删除旧名 CC-Switch / Hermes-Native / Codex-Native / ClaudeCode
  * 统一为新工具名: Codex / AntiGravity / Hermes / Claude / OpenCode
  * Claude / OpenCode 路径: ~/.cc-switch/cc-switch.db (实际只走 cc-switch 代理)
  * 删除"类型"列避免误导 (Codex/Hermes 都有直连+代理两路, 表格写不全)

### v1.3.78 (2026-06-26)

**重构**
- 工具分类聚合: Claude Desktop (cc-switch app_type=claude-desktop) + Claude Code CLI (app_type=claude) 合并为 "Claude", 不再区分 desktop/cli
- 新增 "OpenCode" 工具分类 (cc-switch app_type=opencode, 之前归 Other)
- 同步 go_build/main.go + go_build/static/index.html (前端 toolColors 加 opencode 颜色)

### v1.3.77 (2026-06-26)

**新功能**
- Win launcher 起第二个 webview 弹"检查更新"小窗 (440x220)
  - 启动时自动调 /api/check-update, 实时显示当前/最新版本
  - 有新版本: 显示"立即去下载"按钮, 点击用 cmd /c start 打开 GitCode release 页
  - 已是最新: 显示绿色"✓ 已是最新版本"
  - 拉取失败: 显示错误 + 不阻塞主仪表盘
- 小窗可独立关掉, 不影响主 webview, 用 go-webview2 Bind() 实现 JS→Go 通信

### v1.3.76 (2026-06-26)

**新功能**
- Win 端 Launcher (WebView2 嵌 UI): 双击 `TokenMonitorLauncher.exe` 起独立窗口显示仪表盘, **不再打开系统默认浏览器** (类似 Mac 端 Swift 壳体验)
- 关闭 launcher 窗口后服务继续后台; 再次双击 launcher 端口复用直接重连
- 主服务加 `--no-browser` flag, 由调用方 (launcher) 负责 UI

**修复**
- 同步 `go_build/static/index.html` 580 行 (Win 端之前看不到 v1.3.71+ 所有 UI 改动)
- Go 端工具归一化加 `claude-desktop` / `claude` 分支
- Go 端 `/api/heatmap_detail` 加 `date` 过滤支持
- Go 端 SQL 加 `cache_creation_tokens` + cap 双计 (跟 Python 端同款 bug 修复)
- Go 端 SessionListResponse 加 `summary` 字段
- 移除重复的"会话详情"按钮 (功能已被热力图下钻覆盖)
- 改"未找到对话内容"提示文案, 说明 cc-switch 代理不存原文的真实原因

### v1.3.75 (2026-06-26)

**修复**
- 修每日详情/会话列表的"工具"和"模型"列文字重叠
  - 根因："Claude-Desktop" 文字宽度 ~95px 超过原"工具"列宽 60px，溢出到"模型"列造成视觉重叠
  - 修复：工具列 60px → 100px，模型列 `1fr` → `minmax(0, 1fr)` 防止长 model 名撑爆

### v1.3.74 (2026-06-26)

**修复**
- 还原 `claude-desktop` / `claude` 等被错归为 "Other" 的工具
  - 根因：scanner.py 三处 cc-switch 工具归一化逻辑只匹配 `antigravity / hermes / codex / code`，`claude-desktop` 不含 "code" 连续子串，走 else 分支变成 "Other"
  - 修复：新增 `_normalize_app_type()` 统一函数，三处路径共用，并改为 `Claude-Desktop` 统一命名
  - 影响：每日详情弹窗和首页大屏按工具维度能正确看到 "Claude-Desktop" 分类

### v1.3.73 (2026-06-26)

**修复**
- 修复 cc-switch 路径下 `input_cached` 远大于 `total_tokens` 的数据 bug
  - 根因：OpenAI 兼容协议（如 minimax-m3）的 `input_tokens` 字段已含 cache 命中部分，而 `cache_read_input_tokens` 是额外报告，scanner 简单相加导致双计
  - 修复：当 `cache_read + cache_creation > input_t` 时 cap 到 `input_t`，按 OpenAI 协议正确归类
  - 影响：会话列表的"缓存命中"列从此显示真实数值

**新功能**
- 热力图默认 90 天 → 365 天，新增 30/90/180/365 Tab 切换
- 热力图顶部 3 个统计卡片：总消耗 / 活跃天数（X/Y + 覆盖率）/ 最高单日（值 + 日期）
- 热力图每格右下角叠加 token 短标签（如 `1.2M` / `234K`），不用悬停也能看数量级
- 关于页"重新检查"按钮：弹窗内一键重新检查更新，不必关闭重开
- 主题切换、亮/暗两套 GitHub 风格 5 档色阶（L0-L4）

**基础设施**
- `release_all.sh` 自动 `git tag + push` + 显式传 `target_commitish`，修复 v1.3.72 release 指向旧 commit 的事故

### v1.3.71 (2026-06-26)

- 修复单实例锁残留导致更新后 server 无法启动
- 新增发版前验证清单
