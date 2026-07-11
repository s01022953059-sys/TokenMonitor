#!/usr/bin/env python3
import os
import sqlite3
import json
import datetime
import urllib.request
import re
import glob
import time

# 数据源路径
# 注: 历史上还设过 ANTIGRAVITY_BRAIN_DIR = ~/.gemini/antigravity/brain,
# 实际冰茶 (Antigravity) 的官方统计走的是下面的 BingchaAI 路径, 那个常量早已
# 没有任何引用, 这里不再保留以免误导。
CC_SWITCH_DB_PATH = os.path.expanduser("~/.cc-switch/cc-switch.db")
ANTIGRAVITY_STATS_PATH = os.path.expanduser(
    "~/Library/Application Support/BingchaAI/usage_stats.json"
)
HERMES_DB_PATH = os.path.expanduser("~/.hermes/state.db")
WORKBUDDY_DB_PATH = os.path.expanduser("~/.workbuddy/workbuddy.db")
WORKBUDDY_PROJECTS_DIR = os.path.expanduser("~/.workbuddy/projects")
CODEX_LOG_DB_PATH = os.path.expanduser("~/.codex/logs_2.sqlite")
CODEX_SESSIONS_DIR = os.path.expanduser("~/.codex/sessions")
CODEX_ARCHIVED_SESSIONS_DIR = os.path.expanduser("~/.codex/archived_sessions")

# 跨数据源去重的时间窗口, 单位秒。
# 同一时间窗口内 + 同 token 量视为同一事件, 只计一次。
# 2 秒足够覆盖"客户端 → 代理 → 数据库落盘"的端到端抖动,
# 又不至于把两次独立的相邻请求合并掉。
DEDUP_WINDOW_SECONDS = 2


def _open_sqlite_readonly(path, attempts=3):
    """只读打开可能正被 Agent 原子替换/WAL 写入的数据库，短暂失败时重试。"""
    last_error = None
    for attempt in range(attempts):
        try:
            conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=2)
            conn.execute("PRAGMA busy_timeout=2000")
            return conn
        except sqlite3.OperationalError as exc:
            last_error = exc
            if attempt + 1 < attempts:
                time.sleep(0.05 * (attempt + 1))
    raise last_error

def get_today_midnight_timestamp():
    """获取今天本地时间零点的时间戳"""
    now = datetime.datetime.now()
    midnight = datetime.datetime(now.year, now.month, now.day, 0, 0, 0)
    return int(midnight.timestamp())

def estimate_tokens(text):
    """
    根据字符特征估算 Token 数量 (与旧版一致)
    """
    if not text or not isinstance(text, str):
        return 0
    non_ascii = re.findall(r'[^\x00-\x7F]', text)
    chinese_tokens = len(non_ascii) * 0.8
    ascii_only = re.sub(r'[^\x00-\x7F]', ' ', text)
    words = ascii_only.split()
    english_tokens = len(words) * 1.3
    total_len = len(text)
    remaining_chars = total_len - len(non_ascii) - sum(len(w) for w in words)
    other_tokens = max(0, remaining_chars) * 0.3
    return int(chinese_tokens + english_tokens + other_tokens)

def get_deepseek_balance():
    """从 cc-switch 中安全提取 DeepSeek 密钥，并请求官网 API 获取账户实时余额。

    不再硬编码 provider id, 而是按 provider_type / name 语义匹配,
    兼容用户重命名 provider 后仍然能拿到 DeepSeek 余额。
    """
    if not os.path.exists(CC_SWITCH_DB_PATH):
        return {"balance": "0.00", "currency": "CNY", "status": "Offline"}

    try:
        conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
        cursor = conn.cursor()
        # 三种常见的字段命名都试一遍: provider_type / name / app_type,
        # 任一字段含 deepseek (大小写不敏感) 即视为 DeepSeek provider。
        cursor.execute(
            """
            SELECT id, settings_config FROM providers
            WHERE LOWER(COALESCE(provider_type, '')) LIKE '%deepseek%'
               OR LOWER(COALESCE(name, '')) LIKE '%deepseek%'
               OR LOWER(COALESCE(app_type, '')) LIKE '%deepseek%'
            """
        )
        rows = cursor.fetchall()
        conn.close()

        if not rows:
            return {"balance": "0.00", "currency": "CNY", "status": "Provider Not Found"}

        # 多条匹配时取第一条能解析出 apiKey 的; 都没有就报 No Key。
        key = ""
        for _, cfg_raw in rows:
            try:
                cfg = json.loads(cfg_raw)
            except (TypeError, ValueError):
                continue
            candidate = cfg.get("apiKey") or cfg.get("api_key")
            if candidate:
                key = candidate
                break

        if not key:
            return {"balance": "0.00", "currency": "CNY", "status": "No Key"}

        # 请求 DeepSeek 官方余额接口
        req = urllib.request.Request(
            "https://api.deepseek.com/user/balance",
            headers={
                "Authorization": f"Bearer {key}",
                "Accept": "application/json"
            }
        )
        with urllib.request.urlopen(req, timeout=5) as response:
            res = json.loads(response.read().decode("utf-8"))
            if res.get("is_available"):
                info = res.get("balance_infos", [{}])[0]
                return {
                    "balance": info.get("total_balance", "0.00"),
                    "currency": info.get("currency", "CNY"),
                    "status": "Active"
                }
    except Exception as e:
        return {"balance": "0.00", "currency": "CNY", "status": f"Error: {str(e)}"}

    return {"balance": "0.00", "currency": "CNY", "status": "Unknown"}

def _cc_token_breakdown(app_type, input_tokens, output_tokens, cache_read, cache_creation):
    """按协议语义拆分 cc-switch usage，并返回输入/输出/总量/缓存。"""
    input_tokens = int(input_tokens or 0)
    output_tokens = int(output_tokens or 0)
    cache_read = int(cache_read or 0)
    cache_creation = int(cache_creation or 0)
    app = str(app_type or "").lower()
    # Anthropic usage 的 input_tokens 不含 cache read/create；OpenAI usage 的
    # input_tokens 已含 cached_tokens。cc-switch 没有统一协议列，结合客户端和
    # 字段关系判断，避免 Claude 大量缓存被漏算。
    separate_cache = "claude" in app or cache_read > input_tokens or cache_creation > 0
    if separate_cache:
        input_cached = cache_read
        input_uncached = input_tokens + cache_creation
        total_input = input_uncached + input_cached
    else:
        input_cached = min(input_tokens, max(0, cache_read))
        input_uncached = max(0, input_tokens - input_cached)
        total_input = input_tokens
    return total_input, output_tokens, total_input + output_tokens, input_cached, input_uncached


def scan_cc_switch_logs(today_start):
    """只读扫描 cc-switch 数据库中今天的 API 请求记录，提取包含缓存细节的高精度 Token 消耗。

    使用 provider_id 和 data_source 修正模型名, 确保统计的是真实使用的模型:
    - proxy 来源: model 字段已是实际后端模型, 直接用
    - codex_session 来源: model 是客户端声明名, 用当前激活 provider 的实际模型覆盖
    """
    logs_data = []

    if not os.path.exists(CC_SWITCH_DB_PATH):
        return logs_data

    try:
        conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
        cursor = conn.cursor()

        query = """
            SELECT created_at, app_type, model, input_tokens, output_tokens,
                   cache_read_tokens, cache_creation_tokens, provider_id, data_source
            FROM proxy_request_logs
            WHERE created_at >= ? AND status_code = 200
            ORDER BY created_at ASC
        """
        cursor.execute(query, (today_start,))
        rows = cursor.fetchall()
        conn.close()

        provider_model_map, active_model_by_app = _load_provider_model_map()

        for created_at, app_type, model, input_t, output_t, cache_read_t, cache_creation_t, provider_id, data_source in rows:
            local_time = datetime.datetime.fromtimestamp(created_at).strftime("%H:%M:%S")
            total_input, output_t, total_t, input_cached, input_uncached = _cc_token_breakdown(
                app_type, input_t, output_t, cache_read_t, cache_creation_t
            )

            # 模型修正: codex_session 来源的 model 是声明名, 不可信
            actual_model = model
            if provider_id and provider_id in provider_model_map:
                actual_model = provider_model_map[provider_id]
            elif data_source == "codex_session" or provider_id == "_codex_session":
                active_model = active_model_by_app.get(app_type)
                if active_model:
                    actual_model = active_model

            logs_data.append({
                "time": local_time,
                "timestamp": created_at,
                # 跟详情/会话列表的 tool 归一化保持一致, 避免首页和列表显示不同名
                "tool": _normalize_app_type(app_type),
                "model": normalize_model_name(actual_model),
                "input_tokens": total_input,
                "output_tokens": output_t,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached
            })
    except Exception as e:
        print(f"[-] 扫描 cc-switch 数据库出错: {e}")

    return logs_data


def _codex_log_entry(timestamp, model, usage, session_id=""):
    """把 Codex Responses API / rollout 的 usage 转成统一事件。"""
    if not isinstance(usage, dict):
        return None
    input_tokens = int(usage.get("input_tokens") or 0)
    output_tokens = int(usage.get("output_tokens") or 0)
    total_tokens = int(usage.get("total_tokens") or (input_tokens + output_tokens))
    details = usage.get("input_tokens_details") or {}
    cached_tokens = int(
        details.get("cached_tokens")
        or usage.get("cached_input_tokens")
        or 0
    )
    cached_tokens = min(input_tokens, max(0, cached_tokens))
    if total_tokens <= 0:
        return None
    timestamp = int(timestamp)
    return {
        "time": datetime.datetime.fromtimestamp(timestamp).strftime("%H:%M:%S"),
        "timestamp": timestamp,
        "tool": "Codex",
        "model": normalize_model_name(model or "Unknown"),
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "total_tokens": total_tokens,
        "input_cached": cached_tokens,
        "input_uncached": max(0, input_tokens - cached_tokens),
        "latency_ms": 0,
        "session_id": session_id or "",
    }


def _parse_codex_timestamp(value):
    if not value:
        return 0
    try:
        return int(datetime.datetime.fromisoformat(
            str(value).replace("Z", "+00:00")
        ).timestamp())
    except (TypeError, ValueError):
        return 0


def _scan_codex_rollouts(start_timestamp):
    """旧版/精简版 Codex 没有 logs_2.sqlite 时，从 rollout JSONL 回退读取。"""
    files = []
    for root in (CODEX_SESSIONS_DIR, CODEX_ARCHIVED_SESSIONS_DIR):
        if not os.path.isdir(root):
            continue
        for path in glob.iglob(os.path.join(root, "**", "*.jsonl"), recursive=True):
            try:
                if os.path.getmtime(path) >= start_timestamp - 86400:
                    files.append(path)
            except OSError:
                continue

    events = []
    uuid_pattern = re.compile(
        r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
    )
    for path in files:
        ids = uuid_pattern.findall(os.path.basename(path))
        session_id = ids[-1] if ids else ""
        current_model = "Unknown"
        previous_cumulative = None
        try:
            with open(path, "r", encoding="utf-8") as stream:
                for line in stream:
                    try:
                        item = json.loads(line)
                    except (TypeError, ValueError):
                        continue
                    payload = item.get("payload") or {}
                    if item.get("type") == "turn_context":
                        current_model = payload.get("model") or "Unknown"
                        continue
                    if item.get("type") != "event_msg" or payload.get("type") != "token_count":
                        continue
                    info = payload.get("info") or {}
                    cumulative = info.get("total_token_usage") or {}
                    if cumulative:
                        cumulative_signature = tuple(
                            int(cumulative.get(key) or 0)
                            for key in (
                                "input_tokens", "cached_input_tokens", "output_tokens",
                                "reasoning_output_tokens", "total_tokens",
                            )
                        )
                        if cumulative_signature == previous_cumulative:
                            continue
                        previous_cumulative = cumulative_signature
                    usage = info.get("last_token_usage")
                    timestamp = _parse_codex_timestamp(item.get("timestamp"))
                    if timestamp < start_timestamp:
                        continue
                    event = _codex_log_entry(timestamp, current_model, usage, session_id)
                    if event:
                        events.append(event)
        except OSError:
            continue
    return _dedup_events(events)


def scan_codex_tokens(start_timestamp):
    """直接读取官方 Codex App 日志，不要求用户安装或启用 cc-switch。"""
    events = []
    seen_response_ids = set()
    if os.path.exists(CODEX_LOG_DB_PATH):
        try:
            conn = _open_sqlite_readonly(CODEX_LOG_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT ts, feedback_log_body, thread_id
                FROM logs
                WHERE ts >= ?
                  AND target = 'codex_api::sse::responses'
                  AND feedback_log_body LIKE '%response.completed%'
                ORDER BY ts ASC, ts_nanos ASC
            """, (start_timestamp,))
            for ts, body, thread_id in cursor.fetchall():
                if not body or not body.startswith("SSE event: "):
                    continue
                try:
                    envelope = json.loads(body[len("SSE event: "):])
                except (TypeError, ValueError):
                    continue
                if envelope.get("type") != "response.completed":
                    continue
                response = envelope.get("response") or {}
                response_id = response.get("id")
                if response_id and response_id in seen_response_ids:
                    continue
                if response_id:
                    seen_response_ids.add(response_id)
                timestamp = int(response.get("completed_at") or ts)
                event = _codex_log_entry(
                    timestamp, response.get("model"), response.get("usage"), thread_id or ""
                )
                if event:
                    events.append(event)
            conn.close()
        except Exception as e:
            print(f"[-] 扫描 Codex 日志数据库出错: {e}")

    # logs_2.sqlite 可能只保留当前 Codex 进程的一小段日志，不能把 rollout
    # 仅当成“数据库完全没有数据”时的回退。两者始终合并后去重，才能覆盖重启前记录。
    return _dedup_events(events + _scan_codex_rollouts(start_timestamp))

def normalize_model_name(raw_model):
    """归一化 model 字符串, 合并 cc-switch 噪声变体。

    cc-switch 在某些场景下会把同一个 model 写成多个变体字符串, 例如
    'qwen3.6-plus' / 'qwen3.6-Plus' (大写 P) / 'qwen3.6-plus-2026-04-02' (带日期)。
    这些是 cc-switch 的写入 bug, 不是真实不同的模型。我们把变体折叠到主名,
    避免数据展示里出现 'qwen3.6-plus' 和 'qwen3.6-Plus' 两个独立条目。

    折叠规则 (按顺序匹配):
    1. 转小写、strip 空白
    2. 去掉 '日期后缀' 形式 -YYYY-MM-DD (cc-switch 版本快照)
    3. 找到最长前缀匹配 (例如 'qwen3.6-plus' 是 'qwen3.6-plus-...' 的前缀)
    """
    if not raw_model:
        return "Other"
    s = str(raw_model).strip().lower()
    # 去掉 '-YYYY-MM-DD' 这种日期后缀
    s = re.sub(r'-\d{4}-\d{2}-\d{2}$', '', s)
    # 别名表: 多个变体指向同一标准名
    aliases = {
        "qwen3.6-plus": "qwen3.6-plus",
        "qwen3.6-plus-vl": "qwen3.6-plus-vl",
        "qwen3.7-plus": "qwen3.7-plus",
        "qwen3.7-max": "qwen3.7-max",
    }
    if s in aliases:
        return aliases[s]
    # 启发式: 如果是 'qwen3.6-plus' 家族, 折叠到 'qwen3.6-plus'
    if s.startswith("qwen3.6-plus") and s != "qwen3.6-plus-vl":
        return "qwen3.6-plus"
    # 兜底: 原样返回 (大小写归一化后的)
    return s


def _normalize_app_type(app_type):
    """统一 app_type -> 显示名, 三条 cc-switch 路径共用, 避免首页和列表显示不一致。

    已知映射: claude-desktop / claude -> Claude (统一为客户端名, 不区分 desktop/cli),
    codex -> Codex, hermes -> Hermes, antigravity -> Antigravity,
    opencode -> OpenCode, 其他/空 -> Other
    """
    if not app_type:
        return "Other"
    t_lower = app_type.lower()
    if "antigravity" in t_lower:
        return "冰茶 AI"
    if "hermes" in t_lower:
        return "Hermes"
    if "workbuddy" in t_lower or "codebuddy" in t_lower:
        return "WorkBuddy"
    if "claude" in t_lower:
        # 不区分 desktop / cli / code, 统一为 Claude
        return "Claude"
    if "opencode" in t_lower:
        return "OpenCode"
    if "codex" in t_lower:
        return "Codex"
    return "Other"

def _load_provider_model_map():
    """从 cc-switch 数据库加载两层模型映射。

    返回 (provider_model_map, active_model_by_app):
    - provider_model_map: {provider_id: actual_model}
      从 providers 表的 settings_config.config 解析每个 provider 实际配置的模型。
    - active_model_by_app: {app_type: actual_model}
      每个 app_type 当前激活 (is_current=1) 的 provider 实际配置的模型。
      用于修正 _codex_session 等内部路径的声明模型。

    背景: cc-switch 的 proxy_request_logs 有两种数据来源:
    1. data_source='proxy': 请求经过 cc-switch 代理转发, model 字段记的是
       实际转发到后端的模型 (已替换), 可以信任。
    2. data_source='codex_session': cc-switch 从 ~/.codex/sessions/ 同步的
       Codex 会话日志, model 字段记的是 Codex 客户端声明的模型名 (如 gpt-5.5),
       不是实际后端模型。需要用当前激活的 Codex provider 模型来覆盖。
    """
    provider_model_map = {}
    active_model_by_app = {}
    if not os.path.exists(CC_SWITCH_DB_PATH):
        return provider_model_map, active_model_by_app

    try:
        conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
        cursor = conn.cursor()
        cursor.execute("SELECT id, app_type, settings_config, is_current FROM providers")
        rows = cursor.fetchall()
        conn.close()

        for provider_id, app_type, cfg_raw, is_current in rows:
            if not cfg_raw:
                continue
            try:
                cfg = json.loads(cfg_raw)
            except (TypeError, ValueError):
                continue
            config_str = cfg.get("config", "")
            if not isinstance(config_str, str):
                continue
            for line in config_str.split("\n"):
                line = line.strip()
                if line.startswith("model ="):
                    m = re.search(r'model\s*=\s*["\']([^"\']+)["\']', line)
                    if m:
                        model_name = m.group(1)
                        provider_model_map[provider_id] = model_name
                        if is_current:
                            active_model_by_app[app_type] = model_name
                    break
    except Exception as e:
        print(f"[-] 加载 provider 模型映射出错: {e}")

    return provider_model_map, active_model_by_app

def _get_ccswitch_today_models():
    """从 cc-switch.db 拿今天出现过的 model 集合, 用于 Antigravity 去重判断。
    返回 set() 表示 cc-switch 不可用 / 没数据。
    """
    if not os.path.exists(CC_SWITCH_DB_PATH):
        return set()
    try:
        today_start = get_today_midnight_timestamp()
        conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
        cursor = conn.cursor()
        cursor.execute("""
            SELECT DISTINCT model FROM proxy_request_logs
            WHERE created_at >= ? AND status_code = 200
        """, (today_start,))
        models = {row[0].lower() for row in cursor.fetchall() if row[0]}
        conn.close()
        return models
    except Exception:
        return set()


def scan_antigravity_tokens(today_start):
    """v1.3.90 起冰茶 AI 降级为数据源, scanner 不再产出 events。

    冰茶 AI 客户端 (BingchaAI) 实际产品定位是 IDE / 代理配置入口, 调 LLM 都
    经过 cc-switch Codex 代理. usage_stats.json 是 BingchaAI 客户端的本地
    累计统计, 跟 cc-switch.db 代理记录**同一批请求** (request 数 精确一致,
    total_tokens 接近), 算两遍就是双计.

    之前 v1.3.89 试过按 model 检查 cc-switch.db 跳过重复, 但发现:
    - BingchaAI stats 的 totalTokens 是 cc-switch 的 ~1.9x (部分请求没经代理)
    - 跨源去重本身精度有限
    - 冰茶 AI 既然只是代理入口, 不该作为独立"工具"维度出现

    修法: 直接 return []. 全部流量归到真实调用工具 (Codex / Claude / Other).
    usage_stats.json 文件**不再被 scanner 读取** (保留文件本身).

    退化: 用户完全没装 cc-switch → 冰茶 AI 数据完全丢失 (无法统计).
    这种情况极罕见, 用户可改在 BingchaAI 客户端 → 设置 → 配 cc-switch provider.
    """
    return []

def scan_hermes_tokens(today_start):
    """只读扫描 Hermes 数据库中今天的会话记录，提取包含缓存细节的高精度 Token 消耗"""
    logs_data = []
    if not os.path.exists(HERMES_DB_PATH):
        return logs_data

    try:
        conn = _open_sqlite_readonly(HERMES_DB_PATH)
        cursor = conn.cursor()
        
        # 筛选今天生成的成功会话 (started_at >= today_start)
        query = """
            SELECT id, COALESCE(NULLIF(ended_at, 0), started_at) AS occurred_at,
                   model, input_tokens, output_tokens,
                   cache_read_tokens, cache_write_tokens
            FROM sessions 
            WHERE COALESCE(NULLIF(ended_at, 0), started_at) >= ?
            ORDER BY occurred_at ASC
        """
        cursor.execute(query, (today_start,))
        rows = cursor.fetchall()
        conn.close()
        
        for session_id, occurred_at, model, input_t, output_t, cache_read_t, cache_write_t in rows:
            local_time = datetime.datetime.fromtimestamp(occurred_at).strftime("%H:%M:%S")
            
            # Hermes 数据库中：
            # input_t: 累积未缓存 (或者最新的输入 context 大小)
            # cache_read_t: 累积已缓存
            input_cached = cache_read_t if cache_read_t else 0
            input_uncached = (input_t or 0) + (cache_write_t or 0)
            total_input = input_uncached + input_cached
            output_tokens = output_t if output_t else 0
            total_t = total_input + output_tokens
            
            logs_data.append({
                "time": local_time,
                "timestamp": int(occurred_at),
                "tool": "Hermes",
                "model": normalize_model_name(model) if model else "Unknown",
                "input_tokens": total_input,
                "output_tokens": output_tokens,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached,
                "latency_ms": 0,
                "session_id": session_id or "",
            })
    except Exception as e:
        print(f"[-] 扫描 Hermes 数据库出错: {e}")
        
    return logs_data

def _workbuddy_usage_value(usage, *keys):
    for key in keys:
        value = usage.get(key)
        if value is not None:
            return int(value or 0)
    return 0


def _scan_workbuddy_projects(start_timestamp):
    """按 AgentsView 口径读取 WorkBuddy 会话 JSONL 中的逐请求 usage。"""
    if not os.path.isdir(WORKBUDDY_PROJECTS_DIR):
        return []
    events = []
    for path in glob.iglob(os.path.join(WORKBUDDY_PROJECTS_DIR, "**", "*.jsonl"), recursive=True):
        try:
            if os.path.getmtime(path) < start_timestamp - 86400:
                continue
            with open(path, "r", encoding="utf-8") as stream:
                for line in stream:
                    try:
                        item = json.loads(line)
                    except (TypeError, ValueError):
                        continue
                    provider = item.get("providerData") or {}
                    usage = provider.get("usage") or provider.get("rawUsage")
                    if not isinstance(usage, dict):
                        continue
                    timestamp_ms = int(item.get("timestamp") or 0)
                    timestamp = timestamp_ms // 1000
                    if timestamp < start_timestamp:
                        continue
                    input_tokens = _workbuddy_usage_value(
                        usage, "inputTokens", "input_tokens", "prompt_tokens"
                    )
                    output_tokens = _workbuddy_usage_value(
                        usage, "outputTokens", "output_tokens", "completion_tokens"
                    )
                    total_tokens = _workbuddy_usage_value(usage, "totalTokens", "total_tokens")
                    if total_tokens <= 0:
                        total_tokens = input_tokens + output_tokens
                    if total_tokens <= 0:
                        continue
                    cached_tokens = 0
                    details = usage.get("inputTokensDetails") or []
                    if isinstance(details, list):
                        cached_tokens = sum(
                            int(detail.get("cached_tokens") or 0)
                            for detail in details if isinstance(detail, dict)
                        )
                    if not cached_tokens:
                        prompt_details = usage.get("prompt_tokens_details") or {}
                        cached_tokens = int(prompt_details.get("cached_tokens") or 0)
                    cached_tokens = min(input_tokens, max(0, cached_tokens))
                    events.append({
                        "time": datetime.datetime.fromtimestamp(timestamp).strftime("%H:%M:%S"),
                        "timestamp": timestamp,
                        "tool": "WorkBuddy",
                        "model": normalize_model_name(provider.get("model") or "Other"),
                        "input_tokens": input_tokens,
                        "output_tokens": output_tokens,
                        "total_tokens": total_tokens,
                        "input_cached": cached_tokens,
                        "input_uncached": max(0, input_tokens - cached_tokens),
                        "latency_ms": 0,
                        "session_id": os.path.splitext(os.path.basename(path))[0],
                    })
        except OSError:
            continue
    return _dedup_events(events)


def _scan_workbuddy_session_usage(today_start):
    """兼容旧版 WorkBuddy：没有项目 JSONL 时用上下文占用量作近似值。"""
    logs_data = []
    if not os.path.exists(WORKBUDDY_DB_PATH):
        return logs_data
    try:
        conn = _open_sqlite_readonly(WORKBUDDY_DB_PATH)
        cursor = conn.cursor()
        # sessions + session_usage 联查, created_at/updated_at 是毫秒时间戳
        today_start_ms = int(today_start) * 1000
        cursor.execute("""
            SELECT s.id, s.model, s.created_at, s.updated_at, su.used
            FROM sessions s
            LEFT JOIN session_usage su ON s.id = su.session_id
            WHERE s.deleted_at IS NULL AND s.updated_at >= ?
            ORDER BY s.updated_at ASC
        """, (today_start_ms,))
        for sid, model, created_at, updated_at, used in cursor.fetchall():
            if not used or used <= 0:
                continue
            # WorkBuddy 的 used 字段是总 token (input+output)
            ts = int((updated_at or created_at or 0) / 1000)
            if ts < today_start:
                continue
            local_time = datetime.datetime.fromtimestamp(ts).strftime("%H:%M:%S")
            # model 格式可能是 "custom-local:MiniMax-M3" 或 "hy3"
            actual_model = model.split(":")[-1] if model and ":" in model else (model or "Unknown")
            logs_data.append({
                "time": local_time,
                "timestamp": ts,
                "tool": "WorkBuddy",
                "model": normalize_model_name(actual_model),
                "input_tokens": used,  # WorkBuddy 只存总量, 不分 input/output
                "output_tokens": 0,
                "total_tokens": used,
                "input_cached": 0,
                "input_uncached": used
            })
        conn.close()
    except Exception as e:
        print(f"[-] 扫描 WorkBuddy 数据库出错: {e}")
    return logs_data


def scan_workbuddy_tokens(today_start):
    """优先读取 WorkBuddy 项目 JSONL，旧版本缺失时才回退会话占用量。"""
    if os.path.isdir(WORKBUDDY_PROJECTS_DIR):
        return _scan_workbuddy_projects(today_start)
    return _scan_workbuddy_session_usage(today_start)

def _dedup_events(events):
    """跨数据源去重: 把同一笔请求在多源里都被记录的事件合并为一条。

    判定规则: 时间戳在 DEDUP_WINDOW_SECONDS 窗口内 + 同 total_tokens
    视为同一事件, 只保留 timestamp 最早的一条。

    注意: 去重 key 不再包含 model, 因为同一笔请求在不同来源里记的模型名
    可能不同 (例如 proxy 记 glm-5.2, codex_session 记 glm-5.1)。
    token 数 (input+output) 完全一致 + 时间相近足以判定为同一事件,
    不会误伤相邻的独立请求 (两次请求 input 完全相同的概率极低)。
    """
    if not events:
        return []
    # 按传入顺序决定来源优先级。调用方把 cc-switch 放在 Codex 官方日志前面，
    # 因为 cc-switch 能给出第三方 provider 的真实模型名。
    recent_by_tokens = {}
    deduped = []
    for ev in events:
        timestamp = int(ev.get("timestamp", 0))
        total_tokens = int(ev.get("total_tokens", 0))
        recent = recent_by_tokens.setdefault(total_tokens, [])
        if any(abs(timestamp - seen_ts) <= DEDUP_WINDOW_SECONDS for seen_ts in recent):
            continue
        recent.append(timestamp)
        deduped.append(ev)
    return sorted(deduped, key=lambda x: x["timestamp"])


def get_today_usage():
    """汇总今日所有的大模型 Token 消耗情况以及 DeepSeek 官方余额。

    三源 (cc-switch / 冰茶 Antigravity / Hermes) 加和后做跨源去重,
    避免同一笔请求被多个数据源重复计入。
    """
    today_start = get_today_midnight_timestamp()

    # 1. cc-switch 优先，官方 Codex 日志用于补全未安装 cc-switch 的用户。
    cc_logs = scan_cc_switch_logs(today_start)
    codex_logs = scan_codex_tokens(today_start)
    antigravity_logs = scan_antigravity_tokens(today_start)
    hermes_logs = scan_hermes_tokens(today_start)
    workbuddy_logs = scan_workbuddy_tokens(today_start)

    # 2. 合并去重 + 按时间戳排序
    all_logs = _dedup_events(
        cc_logs + codex_logs + antigravity_logs + hermes_logs + workbuddy_logs
    )

    # 3. 统计汇总
    total_tokens = 0
    input_tokens = 0
    output_tokens = 0
    input_cached = 0
    input_uncached = 0

    # 初始化工具统计 (动态支持从日志中提取的各种客户端如 hermes, codex 等)
    by_tool = {}
    by_model = {}

    for log in all_logs:
        t_tokens = log["total_tokens"]
        i_tokens = log["input_tokens"]
        o_tokens = log["output_tokens"]
        i_cached = log["input_cached"]
        i_uncached = log["input_uncached"]
        tool = log["tool"]
        model = log["model"]

        total_tokens += t_tokens
        input_tokens += i_tokens
        output_tokens += o_tokens
        input_cached += i_cached
        input_uncached += i_uncached

        # 累加按工具
        if tool not in by_tool:
            by_tool[tool] = {"total_tokens": 0, "input_tokens": 0, "output_tokens": 0}
        by_tool[tool]["total_tokens"] += t_tokens
        by_tool[tool]["input_tokens"] += i_tokens
        by_tool[tool]["output_tokens"] += o_tokens

        # 累加按模型
        by_model[model] = by_model.get(model, 0) + t_tokens

    # 获取 DeepSeek 官方实时余额
    ds_balance = get_deepseek_balance()

    return {
        "summary": {
            "total_tokens": total_tokens,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "input_cached": input_cached,
            "input_uncached": input_uncached,
            "date": datetime.datetime.now().strftime("%Y-%m-%d"),
            "deepseek_balance": ds_balance.get("balance", "0.00"),
            "deepseek_currency": ds_balance.get("currency", "CNY"),
            "deepseek_status": ds_balance.get("status", "Offline"),
            # 去重后的事件条数, 给前端做"是否发生跨源重复"的提示
            "events_after_dedup": len(all_logs),
            "events_before_dedup": len(cc_logs) + len(codex_logs) + len(antigravity_logs) + len(hermes_logs) + len(workbuddy_logs),
        },
        "by_tool": by_tool,
        "by_model": by_model,
        # 最新 30 条事件日志
        "recent_events": all_logs[-30:]
    }

def _get_historical_usage_legacy(days=30):
    """获取过去 days 天内每天的 Token 消耗数据，支持工具和模型两个维度细分统计。

    使用 provider_id -> 实际配置模型的映射, 确保统计的是真实使用的模型,
    而非客户端声明的模型名。模型列表动态收集, 不再硬编码, 避免新模型被丢进 Other。
    """
    now = datetime.datetime.now()
    start_date = now - datetime.timedelta(days=days - 1)
    start_date_midnight = datetime.datetime(start_date.year, start_date.month, start_date.day, 0, 0, 0)
    start_timestamp = int(start_date_midnight.timestamp())

    # 准备近 days 天的日期列表
    date_list = []
    for i in range(days):
        d = start_date_midnight + datetime.timedelta(days=i)
        date_list.append(d.strftime("%Y-%m-%d"))

    # 工具归一化映射
    # v1.3.91 修复: 加 OpenCode (cc-switch.db 里有 11 条 opencode app_type 请求)
    # v1.3.90 移除 "冰茶 AI" 项: 冰茶 AI 客户端只是 IDE/代理入口, scanner 不再
    # 单独算它 (避免跟 cc-switch Codex 代理的双计). 流量归到真实调用工具.
    tools = ["Hermes", "Codex", "Claude", "OpenCode", "Other"]

    def get_normalized_tool(app_type):
        return _normalize_app_type(app_type)

    # 加载 provider_id -> 实际配置模型的映射 + app_type -> 当前激活 provider 模型
    provider_model_map, active_model_by_app = _load_provider_model_map()

    # 第一阶段: 扫描所有数据源, 收集实际出现的模型名 (经过 provider 修正 + normalize_model_name)
    all_model_names = set()

    def resolve_model(provider_id, raw_model, data_source=None, app_type=None):
        """用 provider 实际配置的模型覆盖声明模型, 再做归一化。"""
        actual_model = raw_model
        if provider_id and provider_id in provider_model_map:
            actual_model = provider_model_map[provider_id]
        elif data_source == "codex_session" or provider_id == "_codex_session":
            active_model = active_model_by_app.get(app_type)
            if active_model:
                actual_model = active_model
        return normalize_model_name(actual_model)

    # --- 扫描 cc-switch 收集模型名 ---
    cc_rows = []
    if os.path.exists(CC_SWITCH_DB_PATH):
        try:
            conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT created_at, input_tokens, output_tokens, app_type, model, provider_id, data_source
                FROM proxy_request_logs
                WHERE created_at >= ? AND status_code = 200
            """, (start_timestamp,))
            cc_rows = cursor.fetchall()
            conn.close()
            for created_at, input_t, output_t, app_type, model, provider_id, data_source in cc_rows:
                all_model_names.add(resolve_model(provider_id, model, data_source, app_type))
        except Exception as e:
            print(f"[-] 历史扫描 cc-switch 出错: {e}")

    # --- 扫描 Hermes 收集模型名 ---
    hermes_rows = []
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = _open_sqlite_readonly(HERMES_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT started_at, input_tokens, output_tokens, cache_read_tokens, model
                FROM sessions
                WHERE started_at >= ?
            """, (start_timestamp,))
            hermes_rows = cursor.fetchall()
            conn.close()
            for started_at, input_t, output_t, cache_read_t, model in hermes_rows:
                all_model_names.add(normalize_model_name(model))
        except Exception as e:
            print(f"[-] 历史扫描 Hermes 出错: {e}")

    # 官方 Codex App 直连日志。cc-switch 仍然排在前面，后续跨源去重时优先
    # 保留其 provider 实际模型名。
    codex_logs = scan_codex_tokens(start_timestamp)
    for event in codex_logs:
        all_model_names.add(event["model"])

    # Antigravity 固定使用 gemini 3.5 flash
    all_model_names.add("gemini 3.5 flash")

    # 构建动态模型列表 (排序保证图表稳定, Other 放最后)
    models = sorted(all_model_names)
    if "Other" in models:
        models.remove("Other")
    if "other" in models:
        models.remove("other")
    models.append("Other")

    # 初始化历史数据库结构
    tool_data = {t: {d: 0 for d in date_list} for t in tools}
    model_data = {m: {d: 0 for d in date_list} for m in models}
    daily_totals = {d: 0 for d in date_list}

    # 第二阶段: 填充数据
    # --- cc-switch 行级去重: 同一笔请求可能同时出现在 proxy 和 codex_session
    # 两个来源, 用 (时间桶, total_tokens) 去重, 保留先出现的 (proxy 优先) ---
    seen_dedup_keys = set()
    cc_rows_deduped = []
    for row in sorted(cc_rows, key=lambda r: r[0]):
        created_at = row[0]
        input_t = row[1]
        output_t = row[2]
        tokens = input_t + output_t
        bucket = created_at // DEDUP_WINDOW_SECONDS
        dkey = (bucket, tokens)
        if dkey in seen_dedup_keys:
            continue
        seen_dedup_keys.add(dkey)
        cc_rows_deduped.append(row)

    # --- 填充 cc-switch 数据 ---
    for created_at, input_t, output_t, app_type, model, provider_id, data_source in cc_rows_deduped:
        d_str = datetime.datetime.fromtimestamp(created_at).strftime("%Y-%m-%d")
        if d_str in daily_totals:
            tokens = input_t + output_t
            daily_totals[d_str] += tokens
            t_norm = get_normalized_tool(app_type)
            tool_data[t_norm][d_str] += tokens
            m_norm = resolve_model(provider_id, model, data_source, app_type)
            if m_norm not in model_data:
                m_norm = "Other"
            model_data[m_norm][d_str] += tokens

    # --- 填充 Codex 官方日志；与 cc-switch 相同请求只算一次 ---
    cc_timestamps_by_tokens = {}
    for created_at, input_t, output_t, *_ in cc_rows_deduped:
        cc_timestamps_by_tokens.setdefault((input_t or 0) + (output_t or 0), []).append(created_at)
    accepted_codex = []
    for event in codex_logs:
        timestamps = cc_timestamps_by_tokens.get(event["total_tokens"], [])
        if any(abs(event["timestamp"] - ts) <= DEDUP_WINDOW_SECONDS for ts in timestamps):
            continue
        accepted_codex.append(event)
    for event in _dedup_events(accepted_codex):
        d_str = datetime.datetime.fromtimestamp(event["timestamp"]).strftime("%Y-%m-%d")
        if d_str in daily_totals:
            tokens = event["total_tokens"]
            daily_totals[d_str] += tokens
            tool_data["Codex"][d_str] += tokens
            m_norm = event["model"] if event["model"] in model_data else "Other"
            model_data[m_norm][d_str] += tokens

    # --- 填充 Hermes 数据 ---
    for started_at, input_t, output_t, cache_read_t, model in hermes_rows:
        d_str = datetime.datetime.fromtimestamp(started_at).strftime("%Y-%m-%d")
        if d_str in daily_totals:
            tokens = (input_t or 0) + (cache_read_t or 0) + (output_t or 0)
            daily_totals[d_str] += tokens
            tool_data["Hermes"][d_str] += tokens
            m_norm = normalize_model_name(model)
            if m_norm not in model_data:
                m_norm = "Other"
            model_data[m_norm][d_str] += tokens

    # --- 填充 Antigravity 数据 ---
    # v1.3.90 起冰茶 AI 降级为数据源, scanner 不再产出 events. 仍保留这段读取
    # 逻辑作为历史/调试用, 写 tool_data 改用 dict.setdefault 防止 KeyError
    if os.path.exists(ANTIGRAVITY_STATS_PATH):
        try:
            with open(ANTIGRAVITY_STATS_PATH, 'r', encoding='utf-8') as f:
                stats = json.load(f)
            records = stats.get("records", {})
            for d_str in daily_totals:
                record = records.get(d_str)
                if record:
                    input_t = record.get("inputTokens", 0)
                    output_t = record.get("outputTokens", 0)
                    tokens = input_t + output_t
                    daily_totals[d_str] += tokens
                    # 注意: 写 tool_data 时用 setdefault, 避免 KeyError
                    # (冰茶 AI 不在 tools list 里, 这条访问会抛错)
                    tool_data.setdefault("Antigravity", {}).setdefault(d_str, 0)
                    tool_data["Antigravity"][d_str] += tokens
                    model_data.setdefault("gemini 3.5 flash", {}).setdefault(d_str, 0)
                    model_data["gemini 3.5 flash"][d_str] += tokens
        except Exception as e:
            print(f"[-] 历史扫描 冰茶 AI 出错: {e}")

    # 转换为按天排序的 values 序列
    res_tool = {}
    for t in tools:
        res_tool[t] = [tool_data[t][d] for d in date_list]

    res_model = {}
    for m in models:
        res_model[m] = [model_data[m][d] for d in date_list]

    return {
        "labels": date_list,
        "values": [daily_totals[d] for d in date_list],
        "by_tool": res_tool,
        "by_model": res_model
    }


def get_historical_usage(days=30):
    """使用与首页、热力图相同的事件集合生成历史趋势。"""
    days = max(1, int(days))
    now = datetime.datetime.now()
    start_date = now - datetime.timedelta(days=days - 1)
    start_midnight = datetime.datetime(
        start_date.year, start_date.month, start_date.day, 0, 0, 0
    )
    start_timestamp = int(start_midnight.timestamp())
    date_list = [
        (start_midnight + datetime.timedelta(days=i)).strftime("%Y-%m-%d")
        for i in range(days)
    ]
    events = _dedup_events(
        scan_cc_switch_logs(start_timestamp)
        + scan_codex_tokens(start_timestamp)
        + scan_antigravity_tokens(start_timestamp)
        + scan_hermes_tokens(start_timestamp)
        + scan_workbuddy_tokens(start_timestamp)
    )

    default_tools = ["Hermes", "Codex", "Claude", "OpenCode", "WorkBuddy", "Other"]
    tools = default_tools + sorted({event["tool"] for event in events} - set(default_tools))
    model_names = sorted({event["model"] for event in events if event["model"] != "Other"})
    models = model_names + ["Other"]
    tool_data = {tool: {date: 0 for date in date_list} for tool in tools}
    model_data = {model: {date: 0 for date in date_list} for model in models}
    daily_totals = {date: 0 for date in date_list}

    for event in events:
        date = datetime.datetime.fromtimestamp(event["timestamp"]).strftime("%Y-%m-%d")
        if date not in daily_totals:
            continue
        tokens = event["total_tokens"]
        daily_totals[date] += tokens
        tool_data[event["tool"]][date] += tokens
        model = event["model"] if event["model"] in model_data else "Other"
        model_data[model][date] += tokens

    return {
        "labels": date_list,
        "values": [daily_totals[date] for date in date_list],
        "by_tool": {tool: [tool_data[tool][date] for date in date_list] for tool in tools},
        "by_model": {model: [model_data[model][date] for date in date_list] for model in models},
    }

if __name__ == "__main__":
    print(json.dumps(get_today_usage(), indent=2))


def get_session_list(days=1, page=1, page_size=50):
    """获取最近 days 天内的会话事件列表 (去重后), 按时间倒序返回。

    每条事件包含: timestamp, time, tool, model, input_tokens,
    output_tokens, total_tokens, input_cached, input_uncached,
    latency_ms (仅 cc-switch 有)。

    用于前端"会话详情"面板, 展示每一条 API 请求的 token 消耗。
    """
    now = datetime.datetime.now()
    start = now - datetime.timedelta(days=days)
    start_midnight = datetime.datetime(start.year, start.month, start.day, 0, 0, 0)
    start_timestamp = int(start_midnight.timestamp())

    events = []

    # --- cc-switch ---
    provider_model_map, active_model_by_app = _load_provider_model_map()
    if os.path.exists(CC_SWITCH_DB_PATH):
        try:
            conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT created_at, input_tokens, output_tokens, cache_read_tokens,
                       cache_creation_tokens, app_type, model, provider_id,
                       data_source, latency_ms, session_id
                FROM proxy_request_logs
                WHERE created_at >= ? AND status_code = 200
                ORDER BY created_at ASC
            """, (start_timestamp,))
            for row in cursor.fetchall():
                created_at, input_t, output_t, cache_read, cache_creation, \
                    app_type, model, provider_id, data_source, latency_ms, sess_id = row

                # 解析实际模型 (同 get_today_usage 逻辑)
                actual_model = model
                if provider_id and provider_id in provider_model_map:
                    actual_model = provider_model_map[provider_id]
                elif data_source == "codex_session" or provider_id == "_codex_session":
                    active_model = active_model_by_app.get(app_type)
                    if active_model:
                        actual_model = active_model
                actual_model = normalize_model_name(actual_model)

                # 工具归一化 (统一函数, 跟首页大屏 by_tool 保持同名)
                tool = _normalize_app_type(app_type)

                total_input, output_t, total_t, i_cached, i_uncached = _cc_token_breakdown(
                    app_type, input_t, output_t, cache_read, cache_creation
                )

                events.append({
                    "timestamp": created_at,
                    "time": datetime.datetime.fromtimestamp(created_at).strftime("%H:%M:%S"),
                    "tool": tool,
                    "model": actual_model,
                    "input_tokens": total_input,
                    "output_tokens": output_t,
                    "total_tokens": total_t,
                    "input_cached": i_cached,
                    "input_uncached": i_uncached,
                    "latency_ms": latency_ms or 0,
                    "session_id": sess_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] session list cc-switch 出错: {e}")

    # --- 官方 Codex App ---
    events.extend(scan_codex_tokens(start_timestamp))

    # --- Hermes ---
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = _open_sqlite_readonly(HERMES_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT id, COALESCE(NULLIF(ended_at, 0), started_at) AS occurred_at,
                       model, input_tokens, output_tokens,
                       cache_read_tokens, cache_write_tokens
                FROM sessions
                WHERE COALESCE(NULLIF(ended_at, 0), started_at) >= ?
                ORDER BY occurred_at ASC
            """, (start_timestamp,))
            for session_id, occurred_at, model, input_t, output_t, cache_read_t, cache_write_t in cursor.fetchall():
                input_cached = cache_read_t if cache_read_t else 0
                input_uncached = (input_t or 0) + (cache_write_t or 0)
                total_input = input_uncached + input_cached
                output_tokens = output_t if output_t else 0
                total_t = total_input + output_tokens
                events.append({
                    "timestamp": occurred_at,
                    "time": datetime.datetime.fromtimestamp(occurred_at).strftime("%H:%M:%S"),
                    "tool": "Hermes",
                    "model": normalize_model_name(model) if model else "Unknown",
                    "input_tokens": total_input,
                    "output_tokens": output_tokens,
                    "total_tokens": total_t,
                    "input_cached": input_cached,
                    "input_uncached": input_uncached,
                    "latency_ms": 0,
                    "session_id": session_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] session list Hermes 出错: {e}")

    # --- WorkBuddy ---
    events.extend(scan_workbuddy_tokens(start_timestamp))

    # --- Antigravity (按天粒度, 无逐条事件, 跳过) ---

    # 去重 (同 _dedup_events 逻辑)
    events = _dedup_events(events)

    # 按时间倒序
    events.sort(key=lambda x: x["timestamp"], reverse=True)

    # 分页
    total = len(events)
    start = (page - 1) * page_size
    end = start + page_size
    paged = events[start:end]
    return {
        "sessions": paged,
        "total": total,
        "page": page,
        "page_size": page_size,
        "total_pages": (total + page_size - 1) // page_size if page_size > 0 else 1,
    }


def get_heatmap_data(days=30):
    """生成活动热力图数据: 按天排列, 每个格子代表一天的 Token 消耗。

    返回:
    {
        "days": [{"date": "2026-05-27", "label": "05-27", "weekday": 0,
                   "tokens": 12345, "month": 5}, ...],
        "max_value": int,
        "start_date": "2026-05-27",
        "end_date": "2026-06-25"
    }
    """
    now = datetime.datetime.now()
    start = now - datetime.timedelta(days=days - 1)
    start_midnight = datetime.datetime(start.year, start.month, start.day, 0, 0, 0)
    start_timestamp = int(start_midnight.timestamp())

    # 按天聚合: date_str -> tokens
    daily_tokens = {}

    events = _dedup_events(
        scan_cc_switch_logs(start_timestamp)
        + scan_codex_tokens(start_timestamp)
        + scan_hermes_tokens(start_timestamp)
        + scan_workbuddy_tokens(start_timestamp)
    )
    for event in events:
        d_str = datetime.datetime.fromtimestamp(event["timestamp"]).strftime("%Y-%m-%d")
        daily_tokens[d_str] = daily_tokens.get(d_str, 0) + event["total_tokens"]

    # 构建按天列表
    day_list = []
    for i in range(days):
        d = start_midnight + datetime.timedelta(days=i)
        d_str = d.strftime("%Y-%m-%d")
        day_list.append({
            "date": d_str,
            "label": d.strftime("%m-%d"),
            "weekday": d.weekday(),  # 0=Monday
            "month": d.month,
            "tokens": daily_tokens.get(d_str, 0),
        })

    max_value = max((d["tokens"] for d in day_list), default=0)

    return {
        "days": day_list,
        "max_value": max_value,
        "start_date": start_midnight.strftime("%Y-%m-%d"),
        "end_date": now.strftime("%Y-%m-%d"),
    }


def get_session_detail(session_id, max_messages=500, timestamp=None, page=1, page_size=20):
    """根据 session_id 从 Codex rollout JSONL 文件中提取对话内容。

    查找路径: ~/.codex/sessions/YYYY/MM/DD/rollout-*<session_id>*.jsonl
    如果 session_id 不在文件名里 (cc-switch 的 session_id 格式不同于 Codex)，
    用 timestamp 近似匹配最近的 rollout 文件。
    返回: { "session_id": str, "messages": [ {role, text, timestamp}, ...] }
    """
    sessions_dir = os.path.expanduser("~/.codex/sessions")
    messages = []

    if not os.path.isdir(sessions_dir):
        return {"session_id": session_id or "", "messages": messages}

    # 递归查找包含 session_id 的 rollout 文件
    rollout_files = []
    for root, dirs, files in os.walk(sessions_dir):
        for fname in files:
            if fname.endswith(".jsonl") and session_id and session_id in fname:
                rollout_files.append(os.path.join(root, fname))

    # 如果 session_id 没匹配到文件，用 timestamp 找最近的
    if not rollout_files and timestamp:
        try:
            target_ts = int(timestamp)
            best_file = None
            best_diff = float("inf")
            for root, dirs, files in os.walk(sessions_dir):
                for fname in files:
                    if not fname.endswith(".jsonl"):
                        continue
                    path = os.path.join(root, fname)
                    mtime = os.path.getmtime(path)
                    diff = abs(mtime - target_ts)
                    if diff < best_diff:
                        best_diff = diff
                        best_file = path
            # 只在 5 秒内才算匹配
            if best_file and best_diff < 600:
                rollout_files = [best_file]
        except (ValueError, TypeError):
            pass

    if not rollout_files:
        return {"session_id": session_id, "messages": messages}

    # 按修改时间排序，取最新的
    rollout_files.sort(key=lambda x: os.path.getmtime(x), reverse=True)
    rollout_path = rollout_files[0]

    try:
        with open(rollout_path, "r", encoding="utf-8") as f:
            for line in f:
                if not line.strip():
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue

                if obj.get("type") != "response_item":
                    continue

                payload = obj.get("payload", {})
                role = payload.get("role", "")
                if role not in ("user", "assistant"):
                    continue

                content_field = payload.get("content", [])
                text = ""
                if isinstance(content_field, list) and content_field:
                    parts = []
                    for item in content_field:
                        if isinstance(item, dict):
                            t = item.get("text", "")
                            if t:
                                parts.append(t)
                        elif isinstance(item, str):
                            parts.append(item)
                    text = "\n".join(parts)
                elif isinstance(content_field, str):
                    text = content_field

                if not text.strip():
                    continue

                # 截断过长的消息
                if len(text) > 5000:
                    text = text[:5000] + "\n...(内容过长已截断)"

                timestamp_str = payload.get("timestamp", "")

                messages.append({
                    "role": role,
                    "text": text,
                    "timestamp": timestamp_str,
                })

                if len(messages) >= max_messages:
                    break
    except Exception as e:
        print(f"[-] session_detail 出错: {e}")

    # 分页
    total = len(messages)
    start = (page - 1) * page_size
    end = start + page_size
    paged = messages[start:end]
    return {
        "session_id": session_id,
        "messages": paged,
        "total": total,
        "page": page,
        "page_size": page_size,
        "total_pages": (total + page_size - 1) // page_size if page_size > 0 else 1,
    }

def get_heatmap_detail(weekday=None, hour=None, days=30, page=1, page_size=50, date=None):
    """返回某天 (date) 或某时段 (weekday+hour) 的 API 调用列表。

    如果 date 不为 None, 按 date 过滤; 否则按 weekday+hour 过滤。
    """
    now = datetime.datetime.now()
    start = now - datetime.timedelta(days=days - 1)
    start_midnight = datetime.datetime(start.year, start.month, start.day, 0, 0, 0)
    start_timestamp = int(start_midnight.timestamp())

    # 如果指定了 date, 计算该天的起始和结束时间戳
    date_start_ts = None
    date_end_ts = None
    if date:
        try:
            d = datetime.datetime.strptime(date, "%Y-%m-%d")
            date_start_ts = int(d.timestamp())
            date_end_ts = int((d + datetime.timedelta(days=1)).timestamp())
        except ValueError:
            pass

    events = []

    # --- cc-switch ---
    provider_model_map, active_model_by_app = _load_provider_model_map()
    if os.path.exists(CC_SWITCH_DB_PATH):
        try:
            conn = _open_sqlite_readonly(CC_SWITCH_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT created_at, input_tokens, output_tokens, cache_read_tokens,
                       cache_creation_tokens, app_type, model, provider_id,
                       data_source, latency_ms, session_id
                FROM proxy_request_logs
                WHERE created_at >= ? AND status_code = 200
                ORDER BY created_at ASC
            """, (start_timestamp,))
            for row in cursor.fetchall():
                created_at, input_t, output_t, cache_read, cache_creation, \
                    app_type, model, provider_id, data_source, latency_ms, sess_id = row

                dt = datetime.datetime.fromtimestamp(created_at)
                if date_start_ts is not None:
                    if created_at < date_start_ts or created_at >= date_end_ts:
                        continue
                elif weekday is not None:
                    if dt.weekday() != weekday or dt.hour != hour:
                        continue

                actual_model = model
                if provider_id and provider_id in provider_model_map:
                    actual_model = provider_model_map[provider_id]
                elif data_source == "codex_session" or provider_id == "_codex_session":
                    active_model = active_model_by_app.get(app_type)
                    if active_model:
                        actual_model = active_model
                actual_model = normalize_model_name(actual_model)

                # 工具归一化 (统一函数, 跟首页大屏 by_tool 保持同名)
                tool = _normalize_app_type(app_type)

                total_input, output_t, total_t, i_cached, i_uncached = _cc_token_breakdown(
                    app_type, input_t, output_t, cache_read, cache_creation
                )

                events.append({
                    "timestamp": created_at,
                    "time": dt.strftime("%m-%d %H:%M:%S"),
                    "tool": tool,
                    "model": actual_model,
                    "input_tokens": total_input,
                    "output_tokens": output_t,
                    "total_tokens": total_t,
                    "input_cached": i_cached,
                    "input_uncached": i_uncached,
                    "latency_ms": latency_ms or 0,
                    "session_id": sess_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] heatmap_detail cc-switch 出错: {e}")

    # --- 官方 Codex App ---
    for event in scan_codex_tokens(start_timestamp):
        dt = datetime.datetime.fromtimestamp(event["timestamp"])
        if date_start_ts is not None:
            if event["timestamp"] < date_start_ts or event["timestamp"] >= date_end_ts:
                continue
        elif weekday is not None and (dt.weekday() != weekday or dt.hour != hour):
            continue
        event = dict(event)
        event["time"] = dt.strftime("%m-%d %H:%M:%S")
        events.append(event)

    # --- Hermes ---
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = _open_sqlite_readonly(HERMES_DB_PATH)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT id, COALESCE(NULLIF(ended_at, 0), started_at) AS occurred_at,
                       model, input_tokens, output_tokens,
                       cache_read_tokens, cache_write_tokens
                FROM sessions
                WHERE COALESCE(NULLIF(ended_at, 0), started_at) >= ?
                ORDER BY occurred_at ASC
            """, (start_timestamp,))
            for session_id, occurred_at, model, input_t, output_t, cache_read_t, cache_write_t in cursor.fetchall():
                dt = datetime.datetime.fromtimestamp(occurred_at)
                if date_start_ts is not None:
                    if occurred_at < date_start_ts or occurred_at >= date_end_ts:
                        continue
                elif weekday is not None:
                    if dt.weekday() != weekday or dt.hour != hour:
                        continue

                input_cached = cache_read_t if cache_read_t else 0
                input_uncached = (input_t or 0) + (cache_write_t or 0)
                total_input = input_uncached + input_cached
                output_tokens = output_t if output_t else 0
                total_t = total_input + output_tokens

                events.append({
                    "timestamp": occurred_at,
                    "time": dt.strftime("%m-%d %H:%M:%S"),
                    "tool": "Hermes",
                    "model": normalize_model_name(model) if model else "Unknown",
                    "input_tokens": total_input,
                    "output_tokens": output_tokens,
                    "total_tokens": total_t,
                    "input_cached": input_cached,
                    "input_uncached": input_uncached,
                    "latency_ms": 0,
                    "session_id": session_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] heatmap_detail Hermes 出错: {e}")

    # --- WorkBuddy ---
    for event in scan_workbuddy_tokens(start_timestamp):
        dt = datetime.datetime.fromtimestamp(event["timestamp"])
        if date_start_ts is not None:
            if event["timestamp"] < date_start_ts or event["timestamp"] >= date_end_ts:
                continue
        elif weekday is not None and (dt.weekday() != weekday or dt.hour != hour):
            continue
        event = dict(event)
        event["time"] = dt.strftime("%m-%d %H:%M:%S")
        events.append(event)

    # 去重
    events = _dedup_events(events)
    # 按时间倒序
    events.sort(key=lambda x: x["timestamp"], reverse=True)

    # 当天统计: 总 token / 调用次数 / 平均延迟 / 最高单条 / 缓存命中合计
    total_tokens = sum(e.get("total_tokens", 0) for e in events)
    total_cached = sum(e.get("input_cached", 0) for e in events)
    call_count = len(events)
    latencies = [e.get("latency_ms", 0) for e in events if e.get("latency_ms", 0) > 0]
    avg_latency = int(sum(latencies) / len(latencies)) if latencies else 0
    max_latency = max(latencies) if latencies else 0
    peak_call = max(events, key=lambda e: e.get("total_tokens", 0), default=None)
    summary = {
        "total_tokens": total_tokens,
        "total_cached": total_cached,
        "call_count": call_count,
        "avg_latency_ms": avg_latency,
        "max_latency_ms": max_latency,
        "peak_tokens": peak_call.get("total_tokens", 0) if peak_call else 0,
        "peak_time": peak_call.get("time", "") if peak_call else "",
    }

    # 分页
    total = len(events)
    start = (page - 1) * page_size
    end = start + page_size
    paged = events[start:end]
    return {
        "sessions": paged,
        "total": total,
        "page": page,
        "page_size": page_size,
        "total_pages": (total + page_size - 1) // page_size if page_size > 0 else 1,
        "summary": summary,
    }
