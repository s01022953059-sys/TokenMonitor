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

macOS 版有完整的原生体验：WKWebView 内嵌网页、状态栏菜单、NSAlert 更新提示、自动下载安装更新包。

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
| **控制台窗口** | Windows 版双击 `启动TokenMonitor.bat` 启动，会弹出一个控制台窗口（保持运行），自动打开浏览器。没有 macOS 那样的 WKWebView 内嵌原生窗口 |
| **无应用内自更新** | macOS 版能自动下载 DMG 并替换 .app，Windows 版没有这个能力。Windows 用户需要手动下载新版 ZIP 替换 |
| **Antigravity 数据源** | Antigravity (冰茶 AI) 的统计数据路径是 macOS 专属的 (`~/Library/Application Support/`)，Windows 上该文件不存在，自动跳过 |
| **单实例锁机制不同** | macOS 用 `fcntl.flock` 文件锁，Windows 用端口占用检测 |
| **check-update 下载地址** | `/api/check-update` 返回的下载地址优先选 `.dmg` 附件，Windows 用户需要手动从 Release 页面下载 `.zip` |
| **版本号来源** | macOS 从 `Info.plist` 读取，Windows 从同目录 `version.txt` 读取（打包时写入），回退到编译时常量 |
| **无 codesign** | Windows EXE 没有代码签名，首次运行可能被 SmartScreen 拦截，需点击"仍要运行" |

## 安装

### macOS

**方式一：DMG 安装**

```bash
# 下载 DMG
curl -L -o "Token Monitor.dmg" \
  "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.51/Token Monitor.dmg"

# 双击挂载, 拖 Token Monitor.app 到 Applications
open "Token Monitor.dmg"
```

**方式二：脚本安装**

```bash
git clone https://gitcode.com/baggiopeng/TokenMonitor.git
cd TokenMonitor
bash install.sh          # 装到 /Applications
bash install.sh --user   # 装到 ~/Applications (无需密码, 静默升级)
```

### Windows

```bash
# 下载 ZIP
curl -L -o TokenMonitor-win.zip \
  "https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.51/TokenMonitor-win.zip"
```

解压后双击 `启动TokenMonitor.bat`，弹出控制台窗口并自动打开浏览器。

手动访问: http://127.0.0.1:15723
停止服务: 关闭控制台窗口

无需安装 Python 或任何运行时，单个 EXE 即可运行。

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

最新版本：[v1.3.74](https://gitcode.com/baggiopeng/TokenMonitor/releases/v1.3.74)

- macOS: [Token Monitor.dmg](https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.74/Token%20Monitor.dmg)
- Windows: [TokenMonitor-win.zip](https://api.gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.3.74/TokenMonitor-win.zip)

## 最近更新

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
