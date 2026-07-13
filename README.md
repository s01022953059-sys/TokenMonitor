# Token Monitor

本地 Token 使用量监控仪表盘。只读扫描多个 AI 工具的数据源，汇总每日 Token 消耗，提供可视化大屏 + 历史趋势 + 应用内自更新。

支持 **macOS** 和 **Windows** 双平台。

当前发布版本：**v1.4.31**。

## 功能

### 数据采集

只读扫描五类数据源，不修改任何原始数据：

- Token Monitor 展示本机日志中已记录的请求，不等同于供应商账号“全部 API Key、全部设备”的账户总量；模型条目悬停可查看本机请求次数

| 数据源 | 路径 | 说明 |
|---|---|---|
| cc-switch | `~/.cc-switch/cc-switch.db` | SQLite，记录所有经过代理的 API 请求 |
| Codex 官方日志 | `~/.codex/logs_2.sqlite` + `~/.codex/sessions/` | SQLite 与 rollout JSONL 始终合并，覆盖 Codex 重启前后的完整记录 |
| Antigravity (冰茶 AI) | `~/Library/Application Support/BingchaAI/usage_stats.json` | 识别该汇总文件，但不独立累加；其请求与 cc-switch/Codex 日志重合，直接加入会双计 |
| Hermes | `~/.hermes/state.db` | SQLite，会话级记录；输入包含 cache read/write，用量日期优先按会话结束时间归属 |
| WorkBuddy (腾讯 CodeBuddy) | `~/.workbuddy/projects/**/*.jsonl` | 逐请求读取 `providerData.usage`；旧版没有项目日志时才回退 SQLite 会话占用近似值 |

所有数据源合并后做**跨源去重**：相差不超过 2 秒且 Token 总量相同的记录视为同一请求，只计一次。cc-switch 记录优先于 Codex 官方日志，以保留第三方 Provider 的真实模型名；Codex rollout 还会按累计 usage 过滤重复事件。没有安装或没有同步 cc-switch 的用户仍可直接统计官方 Codex App。

**缓存口径**：OpenAI 协议的输入 Token 已包含缓存命中量；Anthropic 协议的 cache read/create 是输入之外的独立字段。Token Monitor 分协议归一化，避免缓存重复计算或漏算。

本地 SQLite 全部以只读方式打开；Agent 正在写入或原子替换数据库时会短暂重试，避免一次瞬时读取失败让整个工具当天显示为 0。

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

### 社区用量排行

- 社区 Dashboard 内展示本机 `User_XXXXX` 匿名 ID、同步状态与排行，不再保留重复的独立 ID 入口
- 安装后自动加入匿名社区统计，启动约 5 秒完成首次上报，之后每小时同步；无需手动加入，“立即同步”仅用于网络失败重试
- 新用户首次打开社区排行时，如果后台首次上报尚未完成，页面会立即登记并自动刷新个人排名
- 展示今日社区总用量、历史参与用户、个人今日用量、完整个人排名和 Top 10；同步过程完全后台化，页面不提供手动同步按钮或传输状态
- 社区页顶部动态栏轮播今日榜首、参与人数、社区总量和热门工具；悬停暂停，并遵循系统的减少动态效果设置
- 社区页使用当天缓存优先展示并在后台刷新，应用启动后静默预取；网络短暂失败时保留最近一次成功数据
- 自动初始化产生的 0 Token 身份只计入历史参与用户，不进入今日榜单或影响今日排名
- 趋势图和热力图同样使用缓存优先展示；热力图始终从同一份 365 天快照切片，冷启动也先显示完整日期网格，历史日志扫描仅在后台静默执行
- 热力图月份轴、星期标签和日期格使用统一固定尺寸，保证 Windows WebView2 与 macOS 下上下对齐
- 状态明确区分“等待首次同步”“今日第 N 名”“已同步但未进前十”和“无法上报”
- 启动约 30 秒后首次自动上报，之后每小时一次；页面聚合结果最多缓存 5 分钟
- 社区报告写入独立的 `community-data` 分支，不污染 `main` 代码提交历史
- 匿名统计通过 `new.taqi.cc` 的 VPS 中继写入 GitCode；其他用户无需安装 Git，也无需配置或接触 GitCode 凭据
- 每台设备首次生成仅保存在本机的随机凭据，用于保护匿名 ID，避免其他设备覆盖该用户的统计
- macOS 与 Windows 新生成的匿名 ID 统一为 `User_` 加 8 位大写字母或数字；旧版短 ID 会在首次安全迁移后换成统一格式
- 旧版无凭据身份迁移时，新报告会记录被替代的旧 ID；之后即使 Token 继续增长，旧报告也不再参与用户数、排名和总量统计
- 社区页支持原地修改公开昵称：昵称与底层匿名 ID 分离，排行榜优先显示昵称，失败不会改变原名称
- 昵称允许中文、英文字母、数字和下划线，长度 2–16 字；全社区大小写不敏感唯一，滚动 24 小时最多修改 3 次，旧昵称保护 30 天
- VPS 统一拦截官方身份冒充、不可见字符、风险词、网址和联系方式；浏览器不接触设备凭据
- 本地改名接口仅接受同源 JSON POST，拒绝第三方网页跨站调用，避免借用本机设备凭据修改昵称
- 不上传对话内容、模型名称、时间戳明细、文件路径或个人信息

### 应用内自更新

- macOS：下载发布包、构建并替换 `.app`，然后自动重启
- macOS 更新不再请求管理员密码：目标目录可写时原地替换，不可写时自动迁移到 `~/Applications`，并按新路径重启
- 发布前验证会覆盖 macOS 原地更新与无权限迁移两条路径，并检查更新脚本不含管理员提权调用
- Windows：下载并校验 Release 中的 `TokenMonitor-Setup.exe`，由安装程序完成升级并重启
- 所有更新入口统一打开“关于 Token Monitor”：进度条固定在更新区顶部，下方仅保留“立即更新”和“稍后”两个操作，不再使用独立更新弹窗
- Windows 首次安装和新版应用内更新统一使用正式安装程序，不再发布 ZIP；Release 中同内容的 `TokenMonitor.exe` 仅用于旧版本在线迁移，不作为手工下载入口
- macOS 内嵌页面使用每次启动生成的临时凭据访问昵称写接口；所有非 HTTP(S) 的 WebKit 本地来源均需通过凭据鉴权，HTTP(S) 仅允许本机回环地址
- About 更新状态使用短句展示，版本号不重复；详细错误保留在悬停提示，检查与更新过程中会清理过期按钮和进度状态

### API 接口

本地 HTTP 服务（默认端口 15723）提供以下主要 API：

| 接口 | 说明 |
|---|---|
| `GET /api/usage` | 今日 Token 汇总（总量/输入/输出/缓存/DeepSeek 余额 + 按工具/模型拆分 + 最近 30 条事件） |
| `GET /api/history` | 过去 30 天每日趋势（按工具/模型拆分） |
| `GET /api/app-info` | 应用信息（名称/版本/更新源） |
| `GET /api/check-update` | 检查更新（请求 GitCode Release API，比较版本号，返回下载地址） |
| `GET /api/session_detail` | 会话详情（按 session_id 或时间戳匹配 Codex rollout 文件，返回对话内容） |
| `GET /api/heatmap_detail` | 热力图详情（按星期 + 小时返回该时段的 API 调用列表） |
| `GET /api/community` | 读取社区今日聚合、个人同步状态和排名 |
| `GET /api/community/report` | 立即提交一次匿名社区统计，并返回真实成功/失败状态 |
| `GET /api/community/optin` | 旧版兼容接口；社区统计始终自动启用 |
| `POST /api/community/profile` | 使用本机设备凭据修改公开社区昵称；请求体仅含 `display_name` |

## 平台实现

### macOS

| 层 | 技术 | 文件 |
|---|---|---|
| UI 壳 | Swift (WKWebView + 状态栏 + 自更新) | `app_wrapper.swift` |
| 后端 | Python (HTTP server + scanner) | `server.py` / `scanner.py` |
| 前端 | HTML/CSS/JS (Chart.js) | `index.html` / `chart.js` |
| 启动 | Bash 脚本 (单实例锁 + nohup) | `start.sh` |
| 安装 | 一站式安装脚本 | `install.sh` |

macOS 版使用 WKWebView 内嵌页面、状态栏菜单和 Dock 图标；更新状态与进度统一显示在 About 页面。

### Windows

| 层 | 技术 | 文件 |
|---|---|---|
| 全部 | Go (HTTP server + scanner) | `go_build/main.go` |
| 前端 | 嵌入的 HTML/CSS/JS (与 macOS 同一份) | `go_build/static/` |
| SQLite | modernc.org/sqlite (纯 Go, 无 CGO) | — |
| 启动 | 单个 GUI EXE (WebView2 + 系统托盘) | `TokenMonitor.exe` |

Windows 版用 Go 交叉编译，无需 Python。运行 `TokenMonitor-Setup.exe` 后安装到当前用户目录，并创建开始菜单快捷方式和标准卸载入口；应用使用内嵌 WebView2 窗口，不打开外部浏览器，也不显示命令行窗口。

- 双击系统托盘图标直接显示应用首页；右键托盘图标打开功能菜单

**Go 版与 Python 版功能完全对齐**，包括：Codex 官方日志与其他本地数据源扫描、跨源去重、模型归一化、DeepSeek 余额语义匹配、check-update 端点、每次请求重读版本号。

## Windows 平台限制

| 限制 | 说明 |
|---|---|
| **首次安全拦截** | macOS 端 Gatekeeper 会拦截未签名 app, Windows 端 SmartScreen 拦截未签名 EXE. 详见下文"绕过安全限制" |
| **WebView2 依赖** | Windows 版使用系统 WebView2 Runtime；较新的 Windows 10/11 通常已内置，缺失时需先安装 Microsoft Edge WebView2 Runtime |
| **Win 端自更新** | 新版下载 `TokenMonitor-Setup.exe`；v1.4.29 及更早版本通过同内容的 `TokenMonitor.exe` 迁移入口静默转交安装器，无需用户重新安装 |
| **Antigravity 数据源** | Antigravity (冰茶 AI) 的统计数据路径是 macOS 专属的 (`~/Library/Application Support/`)，Windows 上该文件不存在，自动跳过 |
| **单实例锁机制不同** | macOS 用 `fcntl.flock`，Windows 用 `LockFileEx` 独占文件锁 |
| **开机自启** | 使用当前用户的 `HKCU\...\Run`，登录后以 `--autostart` 静默启动到托盘，不需要管理员权限；新版会清理旧快捷方式和旧计划任务 |
| **版本号来源** | macOS 从 `Info.plist` 读取；Windows 正式包优先使用编译进 EXE 的版本号，避免旧 `version.txt` 干扰更新判断 |
| **无 codesign / notarize** | 个人项目无 Apple 开发者账号 ($99/yr) / Win 代码签名证书 ($200-500/yr), 用户首次需手动绕过 Gatekeeper / SmartScreen |
| **社区数据上报** | 公开排行可直接查看；匿名统计经 HTTPS VPS 中继提交，不要求用户安装 Git 或配置 GitCode 凭据。中继不可用时会显示同步失败并在下个周期重试 |

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
后续升级 (v1.3.51 → v1.3.92) 因 app 内有自更新, 装新版本后**同样需要重新做一次右键"打开"** (Gatekeeper 重新检查).
如果 v1.3.92 之前装过老版本 (.accessory 模式, dock 没图标), lsregister 重索引 + killall Dock 即可让 dock 重新出现 T+火焰 图标.

#### Windows (SmartScreen)

第一次运行 `TokenMonitor-Setup.exe` 可能会弹:

```
Windows 已保护你的电脑
Microsoft Defender SmartScreen 阻止了无法识别的应用启动
```

**绕过步骤:**
1. 点击弹窗里的 **"更多信息"** (左下角小字)
2. 出现 **"仍要运行"** 按钮, 点击
3. 弹"你确定要运行此应用吗" → 点"是"
4. 之后双击就能正常打开了 (SmartScreen 记住你的选择)

**WebView2 运行时**: `TokenMonitor.exe` 内嵌 WebView2 显示仪表盘，要求 Windows 10 1809+ (Build 17763) 或更高。
大部分 Win10/11 自带 WebView2; 如果是精简版, 下载安装:
<https://developer.microsoft.com/microsoft-edge/webview2/>

### macOS

**方式一: DMG 安装**

```bash
# 下载 DMG
curl -L -o "Token Monitor.dmg" \
  "https://gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.4.31/Token%20Monitor.dmg"

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
# 下载安装程序
curl -L -o TokenMonitor-Setup.exe \
  "https://gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.4.31/TokenMonitor-Setup.exe"
```

双击 `TokenMonitor-Setup.exe`：
- 打开独立 WebView2 仪表盘，不打开外部浏览器、不显示命令行窗口
- 关闭窗口后继续驻留系统托盘，可从托盘重新显示或退出
- 托盘菜单可启用“开机自启”，登录后只启动托盘
- “关于 Token Monitor”内统一检查、更新和展示进度；菜单栏“检查更新…”也直接进入该窗口

调试接口仍可访问 http://127.0.0.1:15723 。
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

### 构建 Windows 安装程序

在 macOS 上直接交叉编译，不需要 Windows 机器：

```bash
bash build_windows.sh  # 交叉编译主程序并嵌入正式安装程序
```

产出：`build/TokenMonitor-Setup.exe`

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
3. Go 交叉编译 Windows 主程序与安装程序 → 上传到同一 Release
4. 清空 `build/` 目录
5. 验证 Release 附件

## 项目结构

```
.
├── app_wrapper.swift          # macOS Swift 壳 (WKWebView + 状态栏 + 自更新)
├── scanner.py                 # macOS 数据采集 (多源事件 + 去重 + 归一化)
├── server.py                  # macOS HTTP 服务 (仪表盘 + 社区 API + 单实例锁)
├── community.py               # macOS 匿名社区上报、聚合与排名
├── index.html                 # 前端大屏 (统计 + 趋势 + 社区排行 + About)
├── chart.js                   # Chart.js v4.5.1
├── start.sh                   # macOS 启动脚本
├── install.sh                 # macOS 安装脚本
├── update_helper.sh           # macOS 自更新 helper
├── build_macos.sh             # macOS .app 构建
├── build_dmg.sh               # macOS DMG 打包
├── build_windows.sh           # Windows 正式安装程序构建
├── release_all.sh             # 统一发布脚本 (Mac + Windows)
├── community_relay/           # VPS 匿名统计中继及 systemd/Nginx 部署模板
├── Info.plist                 # 版本号 + 端口 + 更新源 URL
├── go_build/
│   ├── main.go                # Windows Go 版主程序 (HTTP + scanner + 托盘)
│   ├── community.go           # Windows 匿名社区上报、聚合与排名
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

两版的数据采集逻辑完全对齐（Codex/cc-switch/Hermes/WorkBuddy 多源事件 + 去重 + 归一化 + DeepSeek 余额），API 返回格式一致，前端同一份 `index.html`。

### GitCode Release 上传

GitCode 的 Release 附件上传是两步流程：
1. `GET /releases/:tag/upload_url?file_name=xxx` → 获取预签名 PUT 地址
2. `PUT` 文件到该地址

凭据从 `git credential fill` (host=gitcode.com) 读取，不硬编码 token。

GitCode 不支持通过 API 删除 release 附件，因此每次发版使用新 tag。

## 下载

最新版本：[v1.4.31](https://gitcode.com/baggiopeng/TokenMonitor/releases/v1.4.31)

- macOS: [Token Monitor.dmg](https://gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.4.31/Token%20Monitor.dmg)
- Windows 安装与自动更新: [TokenMonitor-Setup.exe](https://gitcode.com/baggiopeng/TokenMonitor/releases/download/v1.4.31/TokenMonitor-Setup.exe)

## 发布与验证规则

- 普通代码修改不自动改版本号、不打 tag、不创建 Release；只有鹏帅明确说“发新版本”后才发布
- 发版前至少执行 Python/Go 单元测试、前端 JS 语法检查、Windows GUI EXE 交叉编译、macOS `.app` 构建
- 启动本地服务验证今日总数、90 天热力图、会话分页、`/api/check-update` 的平台资产选择
- 使用浏览器实际打开 About 页，验证更新检查、进度、错误状态以及桌面/移动端布局
- Windows 注册表自启、退出替换和重启属于系统行为，正式发布前仍需在真实 Windows 机器完成一次验收
- `bash verify_release.sh` 封装上述自动化基础检查，并验证社区中继公网健康状态和公开榜单读取；`release_all.sh` 会在创建 tag 或 Release 前强制执行，并在上传后重新下载校验 DMG 和 Windows 安装程序，任一项失败就终止发布
- 热力图发布前必须验证默认选中范围与请求参数一致、近一年返回 365 个日格、四个范围切换后的起止日期正确，以及缓存命中低于 500ms
- 发布验证采用三层门禁：充分的单元测试、Python/macOS 与 Go/Windows 双后端 API 契约测试、少量关键用户路径 E2E；详见 [`tests/README.md`](tests/README.md)
- 社区功能变更还必须验证 VPS 健康检查、两个独立匿名用户的新建与更新、错误凭据拒绝，以及 GitCode `community-data` 分支可读回；任一项失败不发布
- 昵称功能变更必须额外验证并发重名、NFKC/大小写冲突、风险名称、24 小时 3 次限额、30 天旧名保护、GitCode 失败回滚，以及桌面/390px 编辑布局

## 最近更新

### v1.4.31 (2026-07-13)

- 热力图前台立即渲染缓存或完整日期网格，历史日志扫描与刷新全部转到后台静默执行。
- 发布流程升级为 Unit、API 契约、E2E、双平台构建四层门禁，防止等待页面和接口不一致进入正式包。

### v1.4.30 (2026-07-13)

- Windows 改为正式用户级安装程序，统一处理首次安装、开始菜单快捷方式、系统卸载入口和应用内升级；新 Release 不再提供 ZIP。

### v1.4.29 (2026-07-13)

- 社区、趋势图和热力图改为缓存优先、后台刷新，缩短页面等待时间；社区人数统一展示历史参与用户，安装后仍会自动加入并后台同步。
- 修正热力图默认范围与界面不一致的问题，并保持首页使用简单的本地日期口径。

### v1.4.28 (2026-07-12)

- 新增社区动态信息条；macOS 更新改为无密码原地替换或自动迁移到用户应用目录

### v1.4.27 (2026-07-12)

- 修正 GPT-5.6 历史事件归类；安装后自动加入社区统计，并通过 VPS 中继同步身份与公开昵称
- 社区昵称改为滚动 24 小时最多修改 3 次，达到限额后明确提示下次可修改时间

### v1.4.26 (2026-07-12)

- 兼容 macOS WebKit 的多种本地来源格式，修复社区昵称修改被误判为跨站请求

### v1.4.25 (2026-07-12)

- 精简 About 更新状态与进度文案，并修复重新检查后的按钮、颜色和进度残留

### v1.4.24 (2026-07-12)

- 修复 macOS 修改社区昵称时被误判为跨站请求，并保留本地接口安全鉴权

### v1.4.23 (2026-07-12)

- 所有更新入口统一进入 About，重做置顶进度条与“立即更新 / 稍后”双按钮布局

### v1.4.22 (2026-07-12)

- 新增社区公开昵称原地编辑，并加入全局重名检测、7 天冷却、旧名保护、风险名称和跨站请求防护

### v1.4.21 (2026-07-12)

- 修复 Codex、WorkBuddy、Hermes 与缓存 Token 的统计口径和跨源重复计算，补齐 Python/Go 回归测试
- 社区匿名统计改经 VPS HTTPS 中继，其他用户无需安装 Git 或配置 GitCode 凭据；增加设备身份保护和多用户链路验证

### v1.4.20 (2026-07-11)

- 修正 GitCode Release API 返回的错误附件域名，确保 macOS DMG、Windows EXE 和 ZIP 可以真实下载

### v1.4.19 (2026-07-11)

- 修复 Windows 开机自启、单实例锁和应用内直接 EXE 更新，更新状态统一在 About 页面展示
- 移除重复的匿名社区 ID 入口，并加入发版前自动验证脚本

### v1.4.18 (2026-07-10)

**社区排行修复与文案优化**
- 修复 GitCode 新建报告错误使用 PUT 导致匿名报告从未创建的问题；按官方 API 改为 POST 新建、PUT 更新
- 修复接口无论上报成功与否都返回 `ok: true`，以及 Windows 总 Token 被错误写成 0 的问题
- 排名改为在全部今日参与者中计算，Top 10 只负责展示
- 页面使用明确的同步状态，移除不准确的“社区累计”“缓存省钱”等表述
- 社区报告迁移到 `community-data` 专用分支，并新增端到端社区链路测试

### v1.4.17 (2026-07-10)

- 新增 WorkBuddy 数据源、社区 Dashboard 和匿名社区 ID 展示
- 修复亮色主题主背景渐变

### v1.3.92 (2026-06-26)

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
