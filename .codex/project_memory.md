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
   - VPS 社区中继变更时, 必须用两个独立本地身份验证新建、更新、错误凭据拒绝和公开聚合读回
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
   - 上传后必须从 `gitcode.com/.../releases/download/...` 重新下载并校验 DMG、PE EXE 和 ZIP；不能只相信 Release API 的附件列表
11. **统计口径不确定时优先参考 AgentsView**: 遇到新 Agent、字段语义、缓存口径、重复事件或会话格式不明确时，先查 [kenn-io/agentsview](https://github.com/kenn-io/agentsview) 对应 parser 和测试，再结合本机原始日志验证；禁止仅凭字段名猜测。

## 架构

- **macOS**: Swift 壳 (app_wrapper.swift) + Python 后端 (scanner.py / server.py) + HTML 前端 (index.html / chart.js)
- **Windows**: Go 单体 (go_build/main.go), 嵌入前端, 系统托盘, 交叉编译 `GOOS=windows GOARCH=amd64`
- **两版功能完全对齐**: Codex 官方日志 + cc-switch / Antigravity / Hermes / WorkBuddy 扫描 + 去重 + 模型归一化 + DeepSeek 余额 + 社区排行 + check-update
- **前端同一份** index.html + chart.js, go_build/static/ 是同步副本

## 社区昵称设计 (2026-07-12, v1.4.22)

- 公开昵称与不可变匿名 ID 分离；改名不得创建新身份或影响历史用量、排名与设备凭据。
- 字符规则: 中文、ASCII 英文字母、数字、下划线，NFKC 后 2–16 字，至少包含中文或英文字母。
- 全局大小写不敏感唯一；滚动 24 小时最多成功修改 3 次，旧昵称保护 30 天。
- VPS SQLite 负责原子重名检测、冷却与旧名保护，GitCode 报告保存公开昵称；写入失败必须回滚。
- 防护系统身份冒充、违规词、不可见/双向字符、XSS、网址和联系方式；风险词表在 VPS 维护。
- 详细设计: `docs/plans/2026-07-12-community-nickname-design.md`。
- VPS 已部署昵称 SQLite 与 `POST /v1/profile`，数据库位于 systemd `StateDirectory=token-monitor-community`，真实目录权限 0750、数据库 0640；Nginx 对改名接口单独限制为 10 次/分钟。
- 线上端到端需验证: 两用户抢同名、同名无变化、大小写重名、错误凭据、风险名称、24 小时 3 次限额、GitCode 昵称和修改时间读回；测试报告与 SQLite 记录需清理。
- 页面使用原地编辑，不增加弹窗；桌面及 390px 窄屏已验证无横向溢出。未修改鹏帅的真实昵称。
- 本地 `POST /api/community/profile` 必须强制 `application/json` 并拒绝非 localhost Origin；CORS 预检不得开放 POST，防止第三方网页借本机凭据改名。

## 数据源

- cc-switch: `~/.cc-switch/cc-switch.db` (SQLite)
- Codex 官方日志: `~/.codex/logs_2.sqlite` 与 `~/.codex/sessions/`、`~/.codex/archived_sessions/` rollout JSONL 始终合并去重
- Antigravity: `~/Library/Application Support/BingchaAI/usage_stats.json` (macOS 专属, Windows 跳过)
- Hermes: `~/.hermes/state.db` (SQLite)
- WorkBuddy: `~/.workbuddy/projects/**/*.jsonl` 的 `providerData.usage` (逐请求准确数据，按 AgentsView 口径); 旧版本没有 projects 时才回退 `~/.workbuddy/workbuddy.db` 的会话占用近似值

## 2026-07-11 Codex 漏统修复 (v1.4.21)

- 根因: 旧实现只通过 cc-switch 的 `proxy_request_logs` 间接统计 Codex，README 虽写了 `~/.codex/logs_2.sqlite`，代码并未读取；未安装或未同步 cc-switch 的官方 Codex App 用户因此显示为 0。
- 修复: Python 与 Windows Go 两端合并读取 `logs_2.sqlite` 的 `response.completed` 与 rollout JSONL 的 `token_count.last_token_usage`。
- 数据源优先级: cc-switch 在前、Codex 官方日志在后。相差不超过 2 秒且 Token 总量相同即合并，优先保留 cc-switch 的第三方 Provider 真实模型名。
- 覆盖范围: 今日首页、历史趋势、会话列表、活动热力图及热力图下钻详情。
- 固定回归: 发版前必须验证“无 cc-switch 仍可统计 Codex”“SQLite 缺失可回退 rollout”“cc-switch + 官方日志不重复计数”；对应 Python/Go 自动化测试已加入仓库。
- **模型历史不可被当前配置覆盖**: cc-switch 的 `_codex_session` 没有具体 provider 身份，必须保留事件自身 model；只有明确 provider_id 才可映射 provider 配置。禁止用当前活动 provider 改写历史事件，否则切换模型后会把 GPT-5.6 等早先流量归到当前模型

## 2026-07-12 统计准确性举一反三审计 (v1.4.21)

- 参考实现: `kenn-io/agentsview`。Codex 以 rollout `last_token_usage` 为逐次用量，WorkBuddy 以项目 JSONL `providerData.usage` 为逐请求用量，缓存 Token 单独归一化。
- Codex 的 `logs_2.sqlite` 只可能覆盖当前进程的一部分，必须始终与 sessions/archived_sessions rollout 合并，不能“SQLite 有数据就停止回退”。
- Codex rollout 的重复 `token_count` 可能累计 usage 完全不变但 last total 非零；按每个文件的累计 usage 签名过滤，否则会多算。
- WorkBuddy `session_usage.used` 是当前上下文占用，不是实际累计消耗；主数据源改为 `~/.workbuddy/projects/**/*.jsonl` 的逐请求 usage，数据库仅作旧版兼容回退。
- cc-switch 同时包含 OpenAI 与 Anthropic 协议：OpenAI input 已含 cached，Anthropic input 不含 cache read/create。必须按协议语义计算，不能统一 cap；旧口径会严重漏算 Claude 缓存输入。
- Python 历史趋势、首页、热力图、会话列表和热力图下钻必须使用同一事件集合；已移除历史趋势对冰茶 JSON 的重复累加，并补齐 WorkBuddy。
- Hermes 总输入应包含 `input_tokens + cache_read_tokens + cache_write_tokens`，用量日期优先采用 `ended_at`（AgentsView 同口径），避免跨天会话归错日期；Python/Go 两端均已对齐。
- 本地 Agent SQLite 可能在 WAL 写入或原子替换瞬间短暂无法打开；Python 统一通过只读连接 + busy timeout + 3 次短重试，禁止单次失败直接把该工具统计成 0。
- 回归门禁新增: Codex 累计重复事件、WorkBuddy providerData usage、OpenAI/Anthropic 缓存语义、历史与热力图同源一致性。

## Windows 限制 (README 中需维护)

- WebView2 内嵌窗口 + 系统托盘，不打开外部浏览器
- 应用内更新直接下载 Release 的 `TokenMonitor.exe`；ZIP 仅供首次/手动安装
- Antigravity 数据源不存在
- 无代码签名 (SmartScreen 拦截)
- 开机自启使用 HKCU Run + `--autostart`，启动后只驻留托盘
- 社区上报通过 `https://new.taqi.cc/token-monitor-community/v1/report` 中继；客户端不安装 Git、不持有 GitCode token，每台设备只在本地保存匿名 ID 与 32 字节随机凭据

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

### v1.4.27 (2026-07-12)
- `_codex_session` 保留事件真实模型，修复 GPT-5.6 被当前 GPT-5.5 配置覆盖
- 新安装及旧 `optin=false` 用户自动加入社区，启动约 5 秒首次上报
- 当前客户端统一经 VPS 中继提交，公开排行榜可读回 `display_name`；旧版直接写 GitCode 的 403 需通过升级消除
- 社区昵称调整为滚动 24 小时最多成功修改 3 次；VPS 使用 `profile_changes` 记录变更历史并完成线上 3 次成功、第 4 次 429 的端到端验证

### v1.4.26 (2026-07-12)
- macOS 社区昵称鉴权由单一 `Origin: null` 判断改为覆盖所有非 HTTP(S) WebKit 本地来源
- 保持临时凭据校验和 HTTP(S) 回环限制，修复误判但不放宽远程跨站访问

### v1.4.25 (2026-07-12)
- About 更新状态改用短句，避免当前版本和下载百分比重复展示
- 修复重新检查及更新失败后的旧按钮、红色样式和进度残留，错误详情保留在悬停提示

### v1.4.24 (2026-07-12)
- 修复 macOS `file://` WebView 修改社区昵称时被来源校验误判的问题
- 使用每次启动生成的临时凭据鉴权 `Origin: null` 请求，未降低跨站防护强度

### v1.4.23 (2026-07-12)
- 更新入口统一进入 About 页面，不再显示其他更新界面
- 更新进度条固定在更新区顶部，下方仅保留“立即更新”和“稍后”两个等宽按钮
- 移除 About 内“下载 zip”按钮，避免操作重复和窄窗口布局错乱

### v1.4.22 (2026-07-12)
- 社区页支持原地修改公开昵称，底层匿名 ID 与设备凭据保持不变
- VPS SQLite 提供全局重名、7 天冷却和旧名 30 天保护，并拦截冒充、风险名称、联系方式与跨站请求

### v1.4.21 (2026-07-12)
- 对齐 AgentsView 与本机原始日志，修复 Codex、WorkBuddy、Hermes、缓存 Token 和跨源去重的统计口径
- 上线 VPS 社区中继，客户端不再依赖 Git/GitCode 凭据；完成多用户鉴权、读回、清理和双平台发版门禁验证

### v1.4.20 (2026-07-11)
- GitCode Release API 返回的附件链接域名 `api.gitcode.com` 实际下载为 404；统一归一化为可下载的 `gitcode.com`，并加入真实下载回归测试

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
- **macOS 静默更新权限**: 禁止仅因目标位于 `/Applications` 就调用 `administrator privileges`。目录可写时直接替换；不可写时迁移到 `~/Applications`，注销旧 LaunchServices 记录并按新路径重启，避免每次更新索要密码和 bundle id 启动到旧副本
- **macOS 更新权限必须回归验证**: 发版前运行 `tests/test_update_helper.sh`，覆盖可写目录原地替换、不可写目录迁移，并检查 helper 不含 `sudo` 或 AppleScript 管理员授权
- **About 更新区布局**: 进度条独立位于更新区顶部，下方只保留“立即更新”和“稍后”两个等宽按钮；不要把进度、阶段文字和按钮放进同一横排
- **About 更新文案**: 当前版本已在左侧展示，右侧状态不得重复版本号；最新版写“已是最新”，新版写“可更新至 vX”，按钮写“立即更新”。错误区只显示“检查失败/更新失败”，详细原因放 `title`；进度标签只显示阶段，百分比单独显示
- **macOS 昵称写入鉴权**: WKWebView 的本地 Origin 不稳定，可能是 `null`、`file://...`、`applewebdata://...` 或其他 WebKit 内部 scheme，不能枚举单个字符串。Swift 每次启动生成临时凭据并同时注入 WebView 与 Python 子进程；所有非 HTTP(S) 来源都必须携带匹配的 `X-Token-Monitor-Client`，HTTP(S) 只允许本机回环地址
- **社区入口保持唯一**: 匿名社区 ID、同步状态和排名统一在社区 Dashboard 展示，不恢复首页独立“我的匿名 ID”按钮或重复弹窗
- **社区安装即加入**: 社区匿名统计随安装自动开启，历史 `community_optin.txt=false` 也自动迁移；启动后约 5 秒首次上报、之后每小时在后台同步，不出现手动加入流程
- **社区同步无感化**: UI 不展示“立即同步”、最近同步时间、等待同步或同步失败等传输过程；只展示社区结果、排名和隐私边界。后台启动后及定时自动上报，失败留在后台重试
- **社区动态展示**: 动态栏只使用已有匿名聚合结果生成榜首、参与人数、总量和工具占比；保持单行、低干扰，悬停暂停并尊重系统减少动态效果，不新增隐私字段

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

### VPS 社区中继 (2026-07-12, v1.4.21)
- macOS/Python 与 Windows/Go 客户端统一向 `https://new.taqi.cc/token-monitor-community/v1/report` 提交匿名数字统计；GitCode token 只保存在 VPS 的 root-only systemd 环境文件中，不进入客户端、仓库或公开接口。
- 每台设备生成 `User_XXXXXXXX` 和 32 字节随机设备凭据；GitCode 报告只保存凭据 SHA-256。相同凭据可更新，错误凭据返回 403，旧版无凭据报告会自动换新匿名 ID。
- Nginx 只公开固定的 HTTPS 中继路径，Go 服务仅监听 `127.0.0.1:18190`；客户端仍从公开 `community-data` 分支读取社区聚合。
- 发版门禁: 中继单测、Python/Go 客户端单测、VPS `/health`、两个独立身份的创建和更新、错误凭据拒绝、GitCode 报告读回及临时测试数据清理全部通过。
- 2026-07-12 实测结果: 两个独立客户端均同步成功，同一身份更新成功，错误凭据返回 HTTP 403，GitCode 数值与上报一致，明文设备凭据未入库，两份测试报告均清理成功。
- 匿名 ID 统一规则: Python/Go 新身份均使用 `User_` + 8 位大写字母或数字；不为了长度主动改写仍有效的旧 ID，旧身份只在安全迁移时换新。
- 身份迁移去重: 新报告持久记录 `replaces_id`；聚合永久排除被替代的旧 ID，不依赖新旧 Token 数继续相同。中继只允许内容与无凭据旧报告一致的首次迁移请求建立该关系。
- 公开仓库报告无需 token 即可读取；写入仅由 VPS 中继持有 GitCode 凭据完成，客户端不接触该凭据
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
