# 已确认决策

## 协作与发版

- 项目内交流称呼用户"鹏帅"。
- 每次更新代码后必须同步刷新 `README.md`（新功能、新限制、版本号、下载地址都要补）。
- **只有鹏帅明确说"发新版本"才能发布**：普通修复**不**改版本号、**不**打 tag、**不**推 Release。
- 版本号两处同步：`Info.plist` 的 `CFBundleShortVersionString` + `go_build/main.go` 的 `var appVersion`。
- 发版流程：先 `bump` → `git commit+tag` → `bash release_all.sh`；`release_all.sh` 内部必须先调 `verify_release.sh`（Unit → API → E2E → 构建）才能进入 tag/Release 步骤。

## 统计与数据

- 统计口径不确定时优先参考 AgentsView (`kenn-io/agentsview`)，禁止仅凭字段名猜测。
- Codex 漏统已修：合并 `logs_2.sqlite` 与 rollout JSONL（v1.4.21）。
- WorkBuddy 改用逐请求 usage（替代历史聚合口径，v1.4.22 审计后落地）。
- 缓存语义：区分"请求内缓存"与"跨请求缓存"，避免重复计费。

## 社区与昵称

- 社区昵称与匿名 ID 分离；24h 内最多 3 次改名；30 天内旧名受保护。
- 测试报告与社区数据隔离（v1.4.32~v1.4.39 持续收紧，本地 / 报告 / 公开聚合三处口径互不污染）。
- 社区弹窗滚动体验：社区排行必须使用固定标题栏和独立内容滚动区；Windows Chromium 用细轨道 + 透明轨道 + 高对比悬停态，同时保留滚轮、拖拽、键盘、触摸板（v1.4.34~v1.4.39 迭代）。
- TOP10 排名变化采用“选择范围即自动演示”：只保留近 30 天、近 90 天、近半年、近一年四段范围，不再提供上一天、下一天或播放/暂停按钮。打开弹窗或切换范围后必须从最早快照自动播放到最新快照并停止，不循环；只有一帧时明确显示“已显示最新”。macOS 与 Windows 对 `days` 参数只接受 `30/90/180/365`，其他输入回退 30 天。

## 系统与锁

- 单实例锁以**内核独占锁为准**；不删除残留锁文件，避免 Windows 上误删活锁。
- `_singleton_check.py` 独立守护单实例。

## 性能与缓存

- **macOS 禁止在多线程服务内 fork**：2026-07-13 v1.4.34 的全年热力图 worker 使用 `multiprocessing` 的 `fork`，macOS 崩溃日志明确记录 `*** multi-threaded process forked ***`，子进程在 SQLite 打开时 SIGSEGV。后台重扫描必须以全新 `server.py --heatmap-worker` 进程执行；测试同时覆盖前台立即返回、worker 写入快照、启动命令不走 fork。
- **每日调用详情缓存口径**：缓存键只能是自然日，后台一次构建完整日快照；`page` 和 `page_size` 只能在快照上切片，绝不能因切换分页或二次打开而重新扫描本地日志。读取异常必须返回明确失败态，前端不得无限停在"加载中"。Unit 覆盖跨页复用与失败收敛，API 契约覆盖不同页大小二次访问的低延迟（v1.4.36~v1.4.39 迭代固化）。
- **单条会话详情性能门禁**：每日列表缓存不等于会话详情缓存。macOS/Python 与 Windows/Go 读取 Codex rollout 时都必须先过滤非 `response_item` 行，避免解析图片、工具输出和统计事件等超大 JSON；消息快照按文件路径、修改时间和大小复用，同一文件的并发首次读取必须合并为一次。Unit 覆盖大无关行、并发合并和跨页复用；Python/Go API 契约覆盖首次打开低于 500ms、二次打开低于 300ms；浏览器 E2E 必须用含大体积无关事件的会话夹具验证首次低于 1 秒、二次低于 300ms 且消息已渲染。真实数据验证必须包含至少 100MB 的本机会话文件，不能只用空 HOME（v1.4.37 修复后落地）。
- **每日调用详情性能门禁**：macOS `/api/heatmap_detail?date=YYYY-MM-DD` 必须按目标自然日限界扫描，Codex rollout 仅查目标日相邻目录；服务启动后台预热当天详情，缓存命中立即返回、过期静默刷新。单元测试需覆盖慢扫描不阻塞，API 契约需覆盖响应阈值，E2E 必须覆盖"热力图点击当天格子后退出加载态"（v1.4.33~v1.4.36 落地）。
- **热力图详情分页只能走按日期路径**：旧版 weekday/hour 详情函数已废弃并移除分页按钮监听；日期详情的上一页、下一页和每页条数按钮各只允许绑定一次，防止翻页时旧监听器用空参数覆盖正确日期，出现 `undefined:00` 或误报"该日暂无调用记录"。前端契约与 E2E 必须覆盖翻到最后一页（v1.4.39 修复）。
- **企业网络兼容**：所有外部更新、社区同步、昵称请求都必须遵循系统代理与 `HTTP(S)_PROXY`/`NO_PROXY`；Windows Go 统一使用 `newProxyHTTPClient`，macOS Python 显式使用 `urllib` 的系统代理 opener。不得把本机回环 API 经代理转发。
- **About 更新摘要规则**：无新版本时显示当前版本摘要；发现新版本后必须显示新版本号与该 Release 的 `notes/body`，不能继续展示旧版本更新内容（v1.4.33 引入）。

## TRAE IDE 接入调研（2026-07-30）

- **D-2026-07-30-01：暂缓接入 TRAE IDE，待条件成熟再复捡。** 鹏帅提出是否支持 Trae IDE（字节跳动 AI IDE），已完成技术调研，结论：数据可得但接入代价高，性价比低。
  - **数据源真相**：Trae CN 的 token 用量**确实在本地**——`~/Library/Application Support/Trae CN/ModularData/ai-agent/database.db`（国际版 `Trae`、`TRAE SOLO CN` 同构）。解密后是 SQLite，token 数据在 `chat_turn` 表的 `context` 字段(JSON)，字段 `prompt_tokens`/`completion_tokens`/`total_tokens`/`reasoning_tokens`/`cache_read_input_tokens`/`cache_creation_input_tokens` + `model_info.config_name`，**与 token_monitor 的 LogEntry 模型完全兼容**。
    - 来源：Trae 官方论坛逆向帖 `forum.trae.cn/t/topic/18327`；已在本机 macOS 亲测确认三个变体文件均存在且为 SQLCipher 加密（前 16 字节 `fd9c 71cc 858a 7ad5...`，明文 sqlite3 报 `file is not a database`）。
    - **纠正** tokscale（`junhoyeo/tokscale`）"Trae 用量本地不存"的说法——它关注的是账号级 API，未碰这个加密库。
  - **核心障碍**：库为 SQLCipher 4 加密（AES-256-CBC，PBKDF2-HMAC-SHA512，256000 次迭代），密钥**不在文件里**而在 Trae 进程内存中，需运行时扫描进程内存提取 + HMAC-SHA512 校验。论坛方案仅 Windows 10/11 + 仅 CN 版，macOS/Linux 不支持。
  - **决策理由**：① 密钥提取把 token_monitor 从"读静态明文文件"拽进"进程内存取证 + SQLCipher 解密"重型领域，与项目"轻量本地监控"定位相悖；② 易被 Trae 升级破坏内存布局；③ macOS 无方案导致**违反"两版功能对齐"硬约束**；④ 国际版/CN 版后端架构不同，单一方案不通用。
  - **参考项**：AgentsView（`kenn-io/agentsview`）支持 20+ 工具但**不支持 Trae**，不能作直接参考；其数据模型(每消息存 input/output/cache_creation/cache_read + 模型名)与 token_monitor 一致，可作口径参照。
  - **备选方向**（未实施）：cc-switch 代理路线（若 Trae 流量能走代理则已被现有数据源间接统计，零成本，优先验证）；或仅做 Windows-only Trae CN 扫描器（需接受两版不对齐）。
  - 发版授权：本调研**不**授权任何版本号变更、tag、Release 或 push。

## AgentMemory 接入

- 轻接入方案：只建 `.agentmemory/` 入口（manifest / brief / current-state / decisions / tasks / glossary / events），**不**迁 `.codex/project_memory.md` 原文，**不**迁 `docs/PROJECT_STATUS.md` 原文。
- 原 `AGENTS.md` 内容**不**改；只在文件末尾追加"AgentMemory 接入"小节。
- 工作区里 7 个 M 文件与本任务无关，**不**自动 add / commit。
- 接入本身**不**等于授权发版；要发版仍需鹏帅明确说"发新版本"。

## 产品设计约束（2026-07-31）

- **D-2026-07-31-01：不展示未采集的字段（反面教材：WorkBuddy "延迟" 列）。** 鹏帅指出 WorkBuddy 调用详情页"延迟"列所有单元格为 `-`、顶部 KPI 平均/最长延迟均为 `0.0s`，并反问"取不到为什么还要给用户看"。结论：UI 上展示"永远拿不到值的字段"是产品设计失误。
  - **错误做法**：保留占位列 + 单元格用 `-` 兜底（WorkBuddy 当前状态）。结果：用户困惑、运营失去发现"采集断流"的入口、UI schema 与数据源不一致。
  - **次优做法**：整列隐藏。问题：掩盖了"采集缺失"这个真问题，未来接入新数据源时失去 UI 占位指引。
  - **✅ 正确做法**：未采集的字段干脆不进入 UI。三选一：
    1. 后端 schema 不返该字段 → 前端自然不渲染（首选）；
    2. 部分采集 → 显示真实数字 + 明确"采样率 X%"；
    3. 计划采集 → 整个模块不进 UI，做完再上。
  - **对本项目的硬约束**：token_monitor **永远不要展示还没采集到的字段**。任何"未来要做"的维度（如延迟、TTFT、首 token 时间等）在后端数据源接通前不进 UI 列表/弹窗/KPI。
  - **反例价值**：WorkBuddy "延迟"列空值是公开的反面教材，下次有人提"要不要加个延迟列"时直接引用本决策。
  - 来源：鹏帅 2026-07-31 在 ZCode 会话中的截图与追问（WorkBuddy v5.3.3 调用详情弹窗）。

## 已接受风险

- **D-2026-09-07-01：`AGENTS.md` 明文 GitCode token 保持现状，不迁移、不轮换。** 来源：鹏帅 2026-09-07 在 Claude Code 会话中明确指示"请忽略"（MAR 启用后首次安全盘点提出）。范围：仅指该 token 的存放方式；不豁免其他凭据的未来安全审查。Agent 不再就此项重复提议，除非 token 泄露造成实际事故或鹏帅改变决定。

## 豆包/豆包工作接入调研（2026-08-27）

- **D-2026-08-27-01：暂缓接入豆包工作（DoubaoWork），本地无 token 数据，复捡条件见附。** 鹏帅提出参考 AgentsView 调研能否支持"豆包工作"统计，已完成调研，结论：**本地根本没有 token 计数字段，接入不可行（而非代价高）**。
  - **AgentsView 横向事实**：`kenn-io/agentsview` 支持 40+ 工具（Aider/Claude Code/Codex/Cursor/Kimi Work/Trae/WorkBuddy/ZCode/Zed 等，全表见 README），**不支持豆包/豆包工作**；源码全库搜 `doubao`/`豆包`/`bytedance` 零命中，issues 无相关请求——没有现成解析实现可参考。
  - **本机实测（macOS）**：装有 `/Applications/DoubaoWork.app`（2.2GB 数据）与 `/Applications/Doubao.app`（Application Support 下仅 public_config.json，数据近乎全在云端）。
  - **数据源真相**：DoubaoWork 是 Chromium 壳，聊天存 `~/Library/Application Support/DoubaoWork/Default/IndexedDB/chrome_doubaowork-chat_0.indexeddb.leveldb`（LevelDB，Blink 编码 + V8 Structured Clone）。全库 strings 探测：只有 `first_token`（首字延迟计时）与 `system_prompt` 等 UI 字段，**不存在 input_tokens/output_tokens/usage 等任何用量数字**；Local Storage 里的 "token" 命中均为 access_token 类鉴权字段。字节埋点库 `Tea/tea.db` 被进程独占且格式私有滚动清理，无稳定口径。
  - **决策理由**：① 豆包/豆包工作面向消费级/办公对话，产品本身不产生也不展示 token 计数（按对话次数/会员额度限流），本地无数字可提；② 若强行接入只能按消息字符数**估算** token，违反 D-2026-07-31-01"不展示未采集字段"的硬约束；③ 解析 Chromium IndexedDB LevelDB（goleveldb + Blink/V8 解码）工程重且随版本升级脆弱，与"轻量本地只读扫描"定位相悖。
  - **复捡触发条件**：① 豆包工作推出本地会话/token 落盘（SQLite/JSONL）或官方用量导出；② 字节开放企业后台用量 API 且鹏帅认可走 API 模式；③ AgentsView 官方宣布支持（届时可直接参考其实现）。
  - 发版授权：本调研**不**授权任何版本号变更、tag、Release 或 push。
