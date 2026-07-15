# Token Monitor 项目记忆

## 核心约定

1. **称呼用户为"鹏帅"**
2. **每次更新代码后同步刷新 README.md** — 新功能、新限制、版本号、下载地址都要更新
3. **版本号两处同步**: `Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`
4. **发布流程**: 鹏帅明确要求发新版本后，bump 版本 → git commit + tag → `bash release_all.sh`（Mac DMG + Windows Setup EXE 一键构建上传）
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
   - Windows `GOOS=windows GOARCH=amd64` 主程序与安装器完整编译，确认 Setup 产物为 PE32+ GUI 且包含主程序
   - macOS 运行 `build_macos.sh`，确认 Swift 编译、图标、签名和版本读取成功
   - 本地 API 冒烟：今日 Token 非 0、热力图日期数量正确、会话分页有数据、check-update 选中正确平台资产
   - Playwright 实际验证 About 更新状态/进度/错误，至少覆盖桌面和移动端
   - **About 当前版本摘要必填**：`index.html` 的 `RELEASE_HIGHLIGHTS` 必须为 `Info.plist` 当前版本提供 1–2 条用户可见短句；`tests/run_unit_tests.sh` 会静态校验，遗漏即阻断发布
   - 涉及 Windows 自启或自替换时，发布前在真实 Windows 机器做一次登录自启、关闭窗口驻留、更新替换重启验收
   - 自动化部分统一执行 `bash verify_release.sh`；`release_all.sh` 必须在任何 tag/Release 操作前调用它，验证失败立即中止
   - 上传后必须从 `gitcode.com/.../releases/download/...` 重新下载并校验 DMG 和 Windows Setup EXE；不能只相信 Release API 的附件列表
   - **测试分层**：Unit 数量最多且覆盖纯逻辑；API 契约测试必须充分，并同时覆盖 Python/macOS 与 Go/Windows 本地进程；E2E 只保留少量关键用户路径，不能用 E2E 数量替代 Unit/API 覆盖。总入口固定为 `bash verify_release.sh`，顺序为 Unit -> API -> E2E -> 构建。
11. **统计口径不确定时优先参考 AgentsView**: 遇到新 Agent、字段语义、缓存口径、重复事件或会话格式不明确时，先查 [kenn-io/agentsview](https://github.com/kenn-io/agentsview) 对应 parser 和测试，再结合本机原始日志验证；禁止仅凭字段名猜测。
12. **每日调用详情性能门禁**：macOS `/api/heatmap_detail?date=YYYY-MM-DD` 必须按目标自然日限界扫描，Codex rollout 仅查目标日相邻目录；服务启动后台预热当天详情，缓存命中立即返回、过期静默刷新。单元测试需覆盖慢扫描不阻塞，API 契约需覆盖响应阈值，E2E 必须覆盖“热力图点击当天格子后退出加载态”。
13. **macOS 禁止在多线程服务内 fork**：2026-07-13 v1.4.34 的全年热力图 worker 使用 `multiprocessing` 的 `fork`，macOS 崩溃日志明确记录 `*** multi-threaded process forked ***`，子进程在 SQLite 打开时 SIGSEGV。后台重扫描必须以全新 `server.py --heatmap-worker` 进程执行；测试同时覆盖前台立即返回、worker 写入快照和启动命令不走 fork。
14. **企业网络兼容**：所有外部更新、社区同步、昵称请求都必须遵循系统代理与 `HTTP(S)_PROXY`/`NO_PROXY`；Windows Go 统一使用 `newProxyHTTPClient`，macOS Python 显式使用 `urllib` 的系统代理 opener。不得把本机回环 API 经代理转发。
15. **About 更新摘要规则**：无新版本时显示当前版本摘要；发现新版本后必须显示新版本号与该 Release 的 `notes/body`，不能继续展示旧版本更新内容。
16. **社区弹窗滚动体验**：社区排行必须使用固定标题栏和独立内容滚动区，禁止让原生滚动条附着在整个弹窗右侧。Windows Chromium 使用细轨道、透明轨道与高对比悬停态，同时保留滚轮、拖拽、键盘和触摸板滚动。
17. **每日调用详情缓存口径**：缓存键只能是自然日，后台一次构建完整日快照；`page` 和 `page_size` 只能在快照上切片，绝不能因切换分页或二次打开而重新扫描本地日志。读取异常必须返回明确失败态，前端不得无限停在“加载中”。单元测试覆盖跨页复用与失败收敛，API 契约覆盖不同页大小二次访问的低延迟。
18. **单条会话详情性能门禁**：每日列表缓存不等于会话详情缓存。macOS/Python 与 Windows/Go 读取 Codex rollout 时都必须先过滤非 `response_item` 行，避免解析图片、工具输出和统计事件等超大 JSON；消息快照按文件路径、修改时间和大小复用，同一文件的并发首次读取必须合并为一次。Unit 覆盖大无关行、并发合并和跨页复用；Python/Go API 契约覆盖首次打开低于 500ms、二次打开低于 300ms；浏览器 E2E 必须用含大体积无关事件的会话夹具验证首次低于 1 秒、二次低于 300ms且消息已渲染。真实数据验证必须包含至少 100MB 的本机会话文件，不能只用空 HOME。
19. **热力图详情分页只能走按日期路径**：旧版 weekday/hour 详情函数已废弃并移除分页按钮监听；日期详情的上一页、下一页和每页条数按钮各只允许绑定一次，防止翻页时旧监听器用空参数覆盖正确日期，出现 `undefined:00` 或误报“该日暂无调用记录”。前端契约与 E2E 必须覆盖翻到最后一页。

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
- Windows 首次安装与新版应用内更新统一下载 `TokenMonitor-Setup.exe`；Release 的同内容 `TokenMonitor.exe` 仅用于 v1.4.29 及更早客户端自动迁移，不再发布 ZIP
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
| build_windows.sh | Windows 主程序与正式安装程序构建 |
| docs/PROJECT_STATUS.md | 详细项目状态 |

## Release Notes 规范

- 每次发版时, release notes 要简短说明本次改动, 一两句话即可
- 格式: 中文, 直接写在 git commit message 里 (release_all.sh 会用 tag 对应的 commit message 作为 release body)
- 示例: `feat: 热力图点击下钻 + 会话详情对话浏览; bump v1.3.65`
- **GitCode 不支持更新已有 release 的 body** (PATCH/PUT 返回 405), 所以 release notes 只在创建时写入, 发版后无法修改
- release_all.sh 已改为: 创建 release 时自动取 `git log -1 --format=%s $TAG` 作为 body

## 功能演进历史

### v1.4.39 (2026-07-15)
- 修复热力图详情翻到最后一页后，旧 weekday/hour 分页监听器覆盖按日期详情，导致标题出现 `undefined:00` 并误报无调用记录。
- 发布前新增“最后一页”浏览器回归测试，并要求热力图详情分页按钮单一绑定。

### v1.4.38 (2026-07-14)
- 修复 macOS 持续高 CPU：网页、托盘和社区轮询统一读取持久化今日快照，每 30 秒最多触发一次后台单飞刷新；SQLite 有查询覆盖时，rollout 补扫严格限制到日期目录。本机 255.6MB 候选日志的今日统计由接近一分钟降至约 0.28 秒。
- Swift 改为直接持有 Python `Process`，退出或自更新前先正常终止、超时再强制回收；真机验证退出后 Python 与 `15723` 端口均消失，稳定状态 Swift/Python CPU 均为 0.0%。

### v1.4.37 (2026-07-14)
- 修复大型 Codex rollout 导致单条调用详情打开超过一分钟：Python/Go 双端先过滤非对话行，再按文件指纹缓存消息快照，并合并同一文件的并发首次读取。
- 发版门禁新增大体积会话 Unit、双后端 API 性能契约与浏览器连续打开两次 E2E；205MB 本机真实会话首次约 14ms、二次约 2ms。

### v1.4.36 (2026-07-14)
- 每日调用详情缓存改为按自然日保存完整快照，二次打开或切换分页只做内存切片；大型 cc-switch 数据库优先按时间索引读取，异常不会无限停留在加载态。
- 社区用户指标说明明确为“去重后的用户数”和“已产生用量的用户数”。

### v1.4.35 (2026-07-13)
- 修复 v1.4.34 macOS 后台热力图 worker 在多线程 Python 服务内 `fork`，导致 SQLite 打开时 SIGSEGV 的崩溃问题；改由全新启动的 `server.py --heatmap-worker` 进程生成快照。
- 社区排行采用固定标题栏和内部内容滚动区，Windows 滚动条收进内容面；更新与社区外部请求兼容系统代理、`HTTP(S)_PROXY` 与 `NO_PROXY`。

### v1.4.33 (2026-07-13)
- macOS 每日调用详情按目标自然日限界扫描，Codex rollout 仅读取相邻日期目录；服务启动后台预热并缓存详情，实测本机扫描从约 6.5 秒降至约 0.06 秒。
- About 增加当前版本 1–2 条离线更新摘要，前端静态契约与 E2E 都会校验发布时没有遗漏该摘要。

### v1.4.32 (2026-07-13)
- 社区统计改为每 5 分钟静默同步，社区页覆盖已上榜用户的补报；修复 macOS 补报 POST 返回 404。
- 页面明确提示新产生的用量可能短暂与首页不同，通常数分钟内自动更新；双端 API 契约覆盖补报接口。

### v1.4.31 (2026-07-13)
- 热力图请求改为完全非阻塞：冷启动也立即返回日期网格，扫描和快照生成均在后台完成
- 发布门禁固定为 Unit、API 契约、E2E、构建四层，并加入慢扫描下前台不阻塞的回归测试

### v1.4.30 (2026-07-13)

- Windows 改为用户级正式安装程序：安装到 LocalAppData、创建开始菜单快捷方式、注册标准卸载入口
- 首次安装和应用内更新统一使用 `TokenMonitor-Setup.exe`，新 Release 不再生成或上传 ZIP

### v1.4.29 (2026-07-13)

- 社区、趋势图和热力图采用当天缓存优先、后台刷新，启动后静默预取常用统计范围
- 社区规模展示历史参与用户，曾成功上报的匿名 ID 会持续计入；今日排名仍只统计今日有效用户
- 热力图默认查询范围修正为近 30 天；首页保持本地日期口径，不增加 UTC 切换

### v1.4.28 (2026-07-12)
- 社区页新增低干扰动态信息条，轮播榜首、参与人数、今日总量和热门工具
- macOS 更新器取消管理员授权，可写时原地更新，不可写时迁移到 `~/Applications`

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
- **Windows 安装与更新统一**: 首次安装和新版应用内升级走 `TokenMonitor-Setup.exe`；为保证旧客户在线升级，Release 还必须上传同内容的 `TokenMonitor.exe` 迁移入口。迁移入口先自复制为临时 Setup 再启动，避免旧替换脚本将其改名为主程序后被 `taskkill` 误杀；不再发布 ZIP 或便携版裸主程序
- **Windows 托盘交互**: 双击托盘图标直接显示并导航首页，右键打开菜单；getlantern/systray 不提供双击回调，通过 Windows 托盘窗口消息子类化处理 `WM_LBUTTONDBLCLK`，左键单击不弹菜单
- **更新 UI 单一入口**: 托盘和原生菜单的更新操作都打开 About 页；后台检查只刷新版本标记，下载进度和错误都在 About 内展示，不使用独立更新弹窗
- **macOS 静默更新权限**: 禁止仅因目标位于 `/Applications` 就调用 `administrator privileges`。目录可写时直接替换；不可写时迁移到 `~/Applications`，注销旧 LaunchServices 记录并按新路径重启，避免每次更新索要密码和 bundle id 启动到旧副本
- **macOS 更新权限必须回归验证**: 发版前运行 `tests/test_update_helper.sh`，覆盖可写目录原地替换、不可写目录迁移，并检查 helper 不含 `sudo` 或 AppleScript 管理员授权
- **About 更新区布局**: 进度条独立位于更新区顶部，下方只保留“立即更新”和“稍后”两个等宽按钮；不要把进度、阶段文字和按钮放进同一横排
- **About 更新文案**: 当前版本已在左侧展示，右侧状态不得重复版本号；最新版写“已是最新”，新版写“可更新至 vX”，按钮写“立即更新”。错误区只显示“检查失败/更新失败”，详细原因放 `title`；进度标签只显示阶段，百分比单独显示
- **macOS 昵称写入鉴权**: WKWebView 的本地 Origin 不稳定，可能是 `null`、`file://...`、`applewebdata://...` 或其他 WebKit 内部 scheme，不能枚举单个字符串。Swift 每次启动生成临时凭据并同时注入 WebView 与 Python 子进程；所有非 HTTP(S) 来源都必须携带匹配的 `X-Token-Monitor-Client`，HTTP(S) 只允许本机回环地址
- **社区入口保持唯一**: 匿名社区 ID、同步状态和排名统一在社区 Dashboard 展示，不恢复首页独立“我的匿名 ID”按钮或重复弹窗
- **社区安装即加入**: 社区匿名统计随安装自动开启，历史 `community_optin.txt=false` 也自动迁移；启动后约 5 秒首次上报、之后每 5 分钟在后台同步，不出现手动加入流程
- **社区页静默补报**: 本地匿名 ID 生成早于首次远端上报；社区页对新用户和已上榜用户都必须按 5 分钟节流后台调用报告接口、清除当天社区缓存并自动刷新，不能要求用户等待定时任务或手工同步
- **热力图轴对齐**: 月份标签和周列必须处于同一横向滚动轨道，并使用同样的 13px 列宽与 2px 间距；近一年末尾月份必须与对应日格对齐，禁止分别计算宽度或额外左侧留白
- **社区同步无感化**: UI 不展示“立即同步”、最近同步时间、等待同步或同步失败等传输过程；只展示社区结果、排名和隐私边界。允许用一条静态说明提示“刚产生的用量可能暂时与首页略有差异，通常几分钟内自动更新”。后台启动后及定时自动上报，失败留在后台重试
- **社区动态展示**: 动态栏只使用已有匿名聚合结果生成榜首、参与人数、总量和工具占比；保持单行、低干扰，悬停暂停并尊重系统减少动态效果，不新增隐私字段
- **社区页面缓存**: 社区 Dashboard 使用当天 `localStorage` 聚合缓存做 stale-while-revalidate，并在应用启动后静默预取；跨日缓存不得展示，刷新失败时保留当天最近成功结果，不缓存错误响应
- **统计页面缓存**: 趋势图使用 `localStorage` 缓存；热力图由双端后端持久保存统一的 365 天快照，30/90/180/365 天从同一快照切片。前台请求绝不扫描日志：快照存在时立即展示（过期快照也先展示），冷启动时立即返回完整空日期网格；扫描仅在后台静默执行，完成后前端自动回填。热力图默认变量必须与 UI 默认 Tab 一致。
- **今日用量轮询缓存**: macOS 的网页、托盘和社区同步不得直接调用全量日志扫描。`/api/usage` 只返回当天持久快照，每 30 秒最多安排一次单飞后台刷新；无论同时有多少请求，同一时刻最多执行一次 `get_today_usage()`。大体积且仍增长的旧会话文件可能包含今日事件，禁止用高频重复全量扫描换取实时性。
- **macOS 后端生命周期**: Swift 必须通过 `Process` 直接启动并持有 `/usr/bin/python3 server.py`，禁止用 `sh -c '... &'` 后只持有瞬时 shell。应用真正退出或进入自更新替换前必须终止并等待 Python，避免窗口退出后后端继续占用 CPU 和 `15723` 端口。
- **热力图发版门禁**: 必须验证默认选中 Tab 与实际请求天数一致、近一年返回恰好 365 个日格、缓存命中接口低于 500ms，并用真实浏览器确认切换范围后日期区间和格子数量同步变化；只验证 HTTP 200 或 JSON 结构不算通过。
- **热力图前台体验**: 任何热力图请求均不得同步扫描历史日志；冷启动须在 500ms 内返回完整日期范围并渲染，后台再生成快照。发布验证必须用慢扫描桩验证 Python/Go 不阻塞，并在 E2E 中确认页面没有停留在“加载中”。
- **macOS 热力图并发**: 全年热力图扫描是 CPU 密集型 Python 工作，不能与 HTTP 服务共用进程/GIL；使用独立进程生成持久快照。启动先预热当天和前一天的详情第一页，再启动全年扫描；冷启动详情接口必须在 500ms 内返回 warming 或缓存，预热完成后命中 ready。
- **桌面单一内容面**: macOS 与 Windows 通过共享前端标识启用单一内容面，不能只改其中一端或让仪表盘外再出现同色页面壳。macOS WebView 顶部必须铺满宿主窗口；Windows 导航必须携带 `desktop=1`。Windows 托盘标题与 macOS 状态栏同口径，每 5 秒展示 `🔥` 加今日 Token 总量。
- **社区参与人数口径**: 顶部社区规模卡片展示“总用户”（去重后的历史匿名 ID）及“今日活跃用户”（当天 `today_tokens > 0` 的唯一 ID）；排行榜和“我的今日排名”使用今日活跃用户作为分母，不能混用两个口径
- **社区测试隔离与零用量口径**: API/E2E 临时服务必须设置 `TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT=1`，禁止临时 HOME 自动上报到真实 VPS。当天 `today_tokens=0` 的启动报告仅保留为历史参与记录，禁止进入今日 Top 10、参与人数、工具占比和排名分母；发现测试污染的远端报告应核实后清理。
- **社区用量同步**: 首页是本机统计，社区是匿名上报快照；两端都必须在启动后和每 5 分钟后台上报，社区页对已上榜用户也要按相同节流静默补报。macOS/Windows 的 `/api/community/report` 都必须接受 POST，前台先显示缓存，绝不能等待扫描完成。
- **供应商用量对账口径**: DeepSeek 控制台按 UTC+0 且可汇总全部 API Key/设备，首页按本机时区且只统计本机日志，二者不能直接比较。首页保持简单，不增加时区切换；有差额时检查其他设备、其他 Key 或绕过 cc-switch 的直连请求
- **AgentViews 统计参考**: 统计准确性问题优先对照 `kenn-io/agentsview` 的实现；沿用其“时区感知日期分桶、缓存读写独立字段、原始本地日志为准”的原则，不用供应商账户聚合值覆盖本机事件
- **社区排行刷新策略**: 常规打开优先展示当天缓存并后台更新；GitCode 独立报告使用最多 8 路有限并发读取，避免用户数增加导致串行等待。标题栏保留明确的“刷新”按钮，用户主动刷新时绕过前后端 5 分钟缓存，但刷新期间继续展示旧数据。

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
## 社区用户口径与去重（2026-07-13）

- 社区卡片只使用两个明确口径：`总用户` 为去重后的全部历史匿名 ID；`今日活跃用户` 为今天已上报且 `today_tokens > 0` 的唯一 ID。0 Token 初始化和尚未当日同步的成员不进入活跃人数、排名或榜单。
- Python 与 Windows Go 聚合均须先做旧身份迁移去重，再按匿名 ID 只保留 `updated_at` 最新的一份报告；绝不能把重复副本累加到人数或 Token。
