# token monitor 项目速览

跨平台的 AI/Agent 用量统计与可视化工具。统计 Codex、Claude Code、WorkBuddy、cc-switch、Antigravity、Hermes 等多个数据源的 token 用量，本地落库后通过 Web 控制台展示，并提供社区昵称、年度热力图、调用详情组合筛选、Windows 安装程序等扩展能力。

## 产品体验

- macOS 端：Swift 壳 (`app_wrapper.swift`) 启动 Python 后端 (`server.py` + `scanner.py`)；前端共用 `index.html` + `chart.js`。
- Windows 端：Go 单体（`go_build/main.go`），独立安装程序。
- 单实例锁保护本地端口；社区昵称与匿名 ID 分离，24h 3 次改名上限，30 天旧名保护。
- 统计口径不确定时优先参考 AgentsView (`kenn-io/agentsview`)，不靠字段名猜测。
- **macOS 禁止多线程服务内 fork**：后台重扫描（全年热力图 worker 等）必须以全新 `server.py --heatmap-worker` 子进程执行；测试同时覆盖前台立即返回、worker 写入快照、启动命令不走 fork（v1.4.34 修复后落地，根因是 macOS `multiprocessing` 的 `fork` 在多线程服务内 SIGSEGV）。
- **每日详情缓存口径**：缓存键只能是自然日，后台一次构建完整日快照；`page`/`page_size` 只能在快照上切片，跨分页或二次打开不得重扫本地日志；读取异常必须收敛到明确失败态，前端不得无限停在"加载中"（v1.4.36~v1.4.39 迭代固化）。
- **单条会话详情性能门禁**：读 Codex rollout 时先过滤非 `response_item` 行（避免图片/工具输出/统计事件超大 JSON），消息快照按文件路径 + 修改时间 + 大小复用；首 <500ms、二次 <300ms；Python/Go API 契约 + 浏览器 E2E 都需覆盖，测试需 100MB+ 真实夹具（v1.4.37 修复后落地）。
- **热力图详情分页只能走按日期路径**：旧版 weekday/hour 详情函数已废弃；日期详情的上一页/下一页/每页条数按钮各只允许绑定一次，防止翻页时旧监听器用空参数覆盖正确日期（v1.4.39 修复）。
- **测试报告与社区数据隔离**（v1.4.32~v1.4.39 持续收紧）：本地 / 报告 / 公开聚合三处口径必须互不污染。
- **企业网络代理**：所有外部更新、社区同步、昵称请求遵循系统代理与 `HTTP(S)_PROXY`/`NO_PROXY`；Windows Go 统一 `newProxyHTTPClient`，macOS Python 用 `urllib` 系统代理 opener；本机回环 API 不得经代理转发。
- **About 更新摘要规则**：无新版本时显示当前版本摘要；发现新版本后必须显示新版本号与该 Release 的 `notes/body`，不能继续展示旧版本更新内容。

## 数据源

- Codex：`logs_2.sqlite` + rollout JSONL 始终合并去重（漏统已修，见 v1.4.21）
- Claude Code：原生日志路径
- WorkBuddy：`~/.workbuddy/projects/**/*.jsonl` 的 `providerData.usage` 逐请求（v1.4.22 审计后落地）；旧版缺 projects 时回退 `workbuddy.db` 会话占用
- cc-switch：`~/.cc-switch/cc-switch.db`（OpenAI input 已含 cache、Anthropic input 不含 cache read/create，按协议语义分别计算）
- Antigravity：`~/Library/Application Support/BingchaAI/usage_stats.json`（macOS 专属）
- Hermes：`~/.hermes/state.db`（输入 = input + cache_read + cache_write；用量日期采用 `ended_at`）
- 缓存语义：区分"请求内缓存"与"跨请求缓存"，避免重复计费
- 本地 SQLite 在 WAL/原子替换瞬间可能短暂打不开，统一只读连接 + busy timeout + 3 次短重试，禁止单次失败直接归 0

## 当前阶段

HEAD `fd17b1c`（v1.4.41 test: 放宽跨平台 API 性能抖动阈值），GitCode `origin/main` 同步到 HEAD，tag `v1.4.33`~`v1.4.41` 已存在。v1.4.32→v1.4.41 共 9 次发版，主线围绕：每日详情加速与缓存复用、大会话详情双端缓存、macOS fork 崩溃 + 高 CPU + 退出残留、热力图分页最后一页、首页圆环扩字段、调用详情组合筛选。工作区除本目录内存同步修改外已 commit 干净，10 个本地 commit 领先 `github/main`，**未** push。
