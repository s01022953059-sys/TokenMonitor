#!/usr/bin/env python3
import os
import sqlite3
import json
import datetime
import urllib.request
import re
import glob

# 数据源路径
# 注: 历史上还设过 ANTIGRAVITY_BRAIN_DIR = ~/.gemini/antigravity/brain,
# 实际冰茶 (Antigravity) 的官方统计走的是下面的 BingchaAI 路径, 那个常量早已
# 没有任何引用, 这里不再保留以免误导。
CC_SWITCH_DB_PATH = os.path.expanduser("~/.cc-switch/cc-switch.db")
ANTIGRAVITY_STATS_PATH = os.path.expanduser(
    "~/Library/Application Support/BingchaAI/usage_stats.json"
)
HERMES_DB_PATH = os.path.expanduser("~/.hermes/state.db")

# 三源去重的"时间窗口", 单位秒。
# 同一时间窗口内 + 同模型 + 同 token 量视为同一事件, 只计一次。
# 2 秒足够覆盖"客户端 → 代理 → 数据库落盘"的端到端抖动,
# 又不至于把两次独立的相邻请求合并掉。
DEDUP_WINDOW_SECONDS = 2

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
        conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
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
        conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
        cursor = conn.cursor()

        query = """
            SELECT created_at, app_type, model, input_tokens, output_tokens,
                   cache_read_tokens, provider_id, data_source
            FROM proxy_request_logs
            WHERE created_at >= ? AND status_code = 200
            ORDER BY created_at ASC
        """
        cursor.execute(query, (today_start,))
        rows = cursor.fetchall()
        conn.close()

        provider_model_map, active_model_by_app = _load_provider_model_map()

        for created_at, app_type, model, input_t, output_t, cache_read_t, provider_id, data_source in rows:
            local_time = datetime.datetime.fromtimestamp(created_at).strftime("%H:%M:%S")
            # 修复: 不同协议对 cache 字段语义不同
            # - Anthropic 协议: input_t = uncached + cache_creation, cache_read 不在 input_t 里
            # - OpenAI 兼容协议: input_t 已含 cache 命中部分, cache_read 是额外报告(双计)
            # 当 cache_read > input_t 时上游是 OpenAI 协议, 把整段 input 视为 cached
            cache_hits = cache_read_t or 0
            if cache_hits > (input_t or 0):
                input_cached = input_t or 0
                input_uncached = 0
            else:
                input_cached = cache_hits
                input_uncached = max(0, (input_t or 0) - cache_hits)
            total_t = (input_t or 0) + (output_t or 0)

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
                "tool": app_type.capitalize() if app_type else "Other",
                "model": normalize_model_name(actual_model),
                "input_tokens": input_t,
                "output_tokens": output_t,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached
            })
    except Exception as e:
        print(f"[-] 扫描 cc-switch 数据库出错: {e}")

    return logs_data

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
        conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
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

def scan_antigravity_tokens(today_start):
    """只读扫描冰茶 AI (Antigravity 本身) 的官方每日统计文件，精确提取今日消耗"""
    tokens_data = []

    if not os.path.exists(ANTIGRAVITY_STATS_PATH):
        return tokens_data

    try:
        with open(ANTIGRAVITY_STATS_PATH, 'r', encoding='utf-8') as f:
            stats = json.load(f)
            
        # 获取今天的日期字符串，格式如 2026-06-04
        today_str = datetime.datetime.now().strftime("%Y-%m-%d")
        records = stats.get("records", {})
        
        # 寻找今天的记录
        today_record = records.get(today_str)
        if today_record:
            input_t = today_record.get("inputTokens", 0)
            output_t = today_record.get("outputTokens", 0)
            cached_t = today_record.get("cachedTokens", 0)
            total_t = input_t + output_t
            
            # 区分缓存
            input_cached = cached_t
            input_uncached = max(0, input_t - cached_t)
            
            tokens_data.append({
                "time": "实时",
                "timestamp": int(datetime.datetime.now().timestamp()),
                "tool": "Antigravity",
                "model": "Gemini 3.5 Flash",
                "input_tokens": input_t,
                "output_tokens": output_t,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached
            })
    except Exception as e:
        print(f"[-] 读取冰茶 AI 统计文件出错: {e}")
        
    return tokens_data

def scan_hermes_tokens(today_start):
    """只读扫描 Hermes 数据库中今天的会话记录，提取包含缓存细节的高精度 Token 消耗"""
    logs_data = []
    if not os.path.exists(HERMES_DB_PATH):
        return logs_data

    try:
        conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
        cursor = conn.cursor()
        
        # 筛选今天生成的成功会话 (started_at >= today_start)
        query = """
            SELECT started_at, model, input_tokens, output_tokens, cache_read_tokens 
            FROM sessions 
            WHERE started_at >= ?
            ORDER BY started_at ASC
        """
        cursor.execute(query, (today_start,))
        rows = cursor.fetchall()
        conn.close()
        
        for started_at, model, input_t, output_t, cache_read_t in rows:
            local_time = datetime.datetime.fromtimestamp(started_at).strftime("%H:%M:%S")
            
            # Hermes 数据库中：
            # input_t: 累积未缓存 (或者最新的输入 context 大小)
            # cache_read_t: 累积已缓存
            input_cached = cache_read_t if cache_read_t else 0
            input_uncached = input_t if input_t else 0
            total_input = input_uncached + input_cached
            output_tokens = output_t if output_t else 0
            total_t = total_input + output_tokens
            
            logs_data.append({
                "time": local_time,
                "timestamp": int(started_at),
                "tool": "Hermes",
                "model": model if model else "Unknown",
                "input_tokens": total_input,
                "output_tokens": output_tokens,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached
            })
    except Exception as e:
        print(f"[-] 扫描 Hermes 数据库出错: {e}")
        
    return logs_data

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
    # 按时间升序, 保证 group key 相同时保留最早的
    ordered = sorted(events, key=lambda x: x["timestamp"])
    seen_keys = set()
    deduped = []
    for ev in ordered:
        bucket = ev["timestamp"] // DEDUP_WINDOW_SECONDS
        key = (bucket, ev.get("total_tokens", 0))
        if key in seen_keys:
            continue
        seen_keys.add(key)
        deduped.append(ev)
    return deduped


def get_today_usage():
    """汇总今日所有的大模型 Token 消耗情况以及 DeepSeek 官方余额。

    三源 (cc-switch / 冰茶 Antigravity / Hermes) 加和后做跨源去重,
    避免同一笔请求被多个数据源重复计入。
    """
    today_start = get_today_midnight_timestamp()

    # 1. 扫描三个独立的数据源
    cc_logs = scan_cc_switch_logs(today_start)
    antigravity_logs = scan_antigravity_tokens(today_start)
    hermes_logs = scan_hermes_tokens(today_start)

    # 2. 合并去重 + 按时间戳排序
    all_logs = _dedup_events(cc_logs + antigravity_logs + hermes_logs)

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
            "events_before_dedup": len(cc_logs) + len(antigravity_logs) + len(hermes_logs),
        },
        "by_tool": by_tool,
        "by_model": by_model,
        # 最新 30 条事件日志
        "recent_events": all_logs[-30:]
    }

def get_historical_usage(days=30):
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
    tools = ["Antigravity", "Hermes", "Codex", "Other"]

    def get_normalized_tool(app_type):
        if not app_type:
            return "Other"
        t_lower = app_type.lower()
        if "antigravity" in t_lower:
            return "Antigravity"
        if "hermes" in t_lower:
            return "Hermes"
        if "codex" in t_lower or "code" in t_lower:
            return "Codex"
        return "Other"

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
            conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
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
            conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
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
                    tool_data["Antigravity"][d_str] += tokens
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
            conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
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

                # 工具归一化
                t_lower = (app_type or "").lower()
                if "antigravity" in t_lower:
                    tool = "Antigravity"
                elif "hermes" in t_lower:
                    tool = "Hermes"
                elif "codex" in t_lower or "code" in t_lower:
                    tool = "Codex"
                else:
                    tool = "Other"

                i_cached_raw = (cache_read or 0) + (cache_creation or 0)
                # 修复: 上游 OpenAI 兼容协议会把 cache 命中 token 重复算, 这里 cap
                # 保证 i_cached <= input_t (input_t 在 OpenAI 协议下已经含 cache 部分)
                if i_cached_raw > (input_t or 0):
                    i_cached = input_t or 0
                    i_uncached = 0
                else:
                    i_cached = i_cached_raw
                    i_uncached = max(0, (input_t or 0) - i_cached)
                total_input = input_t or 0
                total_t = total_input + (output_t or 0)

                events.append({
                    "timestamp": created_at,
                    "time": datetime.datetime.fromtimestamp(created_at).strftime("%H:%M:%S"),
                    "tool": tool,
                    "model": actual_model,
                    "input_tokens": total_input,
                    "output_tokens": output_t or 0,
                    "total_tokens": total_t,
                    "input_cached": i_cached,
                    "input_uncached": i_uncached,
                    "latency_ms": latency_ms or 0,
                    "session_id": sess_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] session list cc-switch 出错: {e}")

    # --- Hermes ---
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT started_at, model, input_tokens, output_tokens, cache_read_tokens
                FROM sessions
                WHERE started_at >= ?
                ORDER BY started_at ASC
            """, (start_timestamp,))
            for started_at, model, input_t, output_t, cache_read_t in cursor.fetchall():
                input_cached = cache_read_t if cache_read_t else 0
                input_uncached = input_t if input_t else 0
                total_input = input_uncached + input_cached
                output_tokens = output_t if output_t else 0
                total_t = total_input + output_tokens
                events.append({
                    "timestamp": started_at,
                    "time": datetime.datetime.fromtimestamp(started_at).strftime("%H:%M:%S"),
                    "tool": "Hermes",
                    "model": normalize_model_name(model) if model else "Unknown",
                    "input_tokens": total_input,
                    "output_tokens": output_tokens,
                    "total_tokens": total_t,
                    "input_cached": input_cached,
                    "input_uncached": input_uncached,
                    "latency_ms": 0,
                    "session_id": "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] session list Hermes 出错: {e}")

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

    # --- cc-switch ---
    seen_dedup = set()
    if os.path.exists(CC_SWITCH_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT created_at, input_tokens, output_tokens
                FROM proxy_request_logs
                WHERE created_at >= ? AND status_code = 200
            """, (start_timestamp,))
            for created_at, input_t, output_t in cursor.fetchall():
                tokens = (input_t or 0) + (output_t or 0)
                bucket = created_at // DEDUP_WINDOW_SECONDS
                dkey = (bucket, tokens)
                if dkey in seen_dedup:
                    continue
                seen_dedup.add(dkey)
                dt = datetime.datetime.fromtimestamp(created_at)
                d_str = dt.strftime("%Y-%m-%d")
                daily_tokens[d_str] = daily_tokens.get(d_str, 0) + tokens
            conn.close()
        except Exception as e:
            print(f"[-] heatmap cc-switch 出错: {e}")

    # --- Hermes ---
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT started_at, input_tokens, output_tokens, cache_read_tokens
                FROM sessions
                WHERE started_at >= ?
            """, (start_timestamp,))
            for started_at, input_t, output_t, cache_read_t in cursor.fetchall():
                tokens = (input_t or 0) + (cache_read_t or 0) + (output_t or 0)
                bucket = started_at // DEDUP_WINDOW_SECONDS
                dkey = (bucket, tokens)
                if dkey in seen_dedup:
                    continue
                seen_dedup.add(dkey)
                dt = datetime.datetime.fromtimestamp(started_at)
                d_str = dt.strftime("%Y-%m-%d")
                daily_tokens[d_str] = daily_tokens.get(d_str, 0) + tokens
            conn.close()
        except Exception as e:
            print(f"[-] heatmap Hermes 出错: {e}")

    # --- Antigravity (按天 JSON) ---
    if os.path.exists(ANTIGRAVITY_STATS_PATH):
        try:
            with open(ANTIGRAVITY_STATS_PATH, 'r', encoding='utf-8') as f:
                stats = json.load(f)
            records = stats.get("records", {})
            for i in range(days):
                d = start_midnight + datetime.timedelta(days=i)
                d_str = d.strftime("%Y-%m-%d")
                record = records.get(d_str)
                if record:
                    tokens = record.get("inputTokens", 0) + record.get("outputTokens", 0)
                    daily_tokens[d_str] = daily_tokens.get(d_str, 0) + tokens
        except Exception as e:
            print(f"[-] heatmap Antigravity 出错: {e}")

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
            conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
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

                t_lower = (app_type or "").lower()
                if "antigravity" in t_lower:
                    tool = "Antigravity"
                elif "hermes" in t_lower:
                    tool = "Hermes"
                elif "codex" in t_lower or "code" in t_lower:
                    tool = "Codex"
                else:
                    tool = "Other"

                i_cached_raw = (cache_read or 0) + (cache_creation or 0)
                # 修复: 上游 OpenAI 兼容协议会把 cache 命中 token 重复算, 这里 cap
                # 保证 i_cached <= input_t (input_t 在 OpenAI 协议下已经含 cache 部分)
                if i_cached_raw > (input_t or 0):
                    i_cached = input_t or 0
                    i_uncached = 0
                else:
                    i_cached = i_cached_raw
                    i_uncached = max(0, (input_t or 0) - i_cached)
                total_input = input_t or 0
                total_t = total_input + (output_t or 0)

                events.append({
                    "timestamp": created_at,
                    "time": dt.strftime("%m-%d %H:%M:%S"),
                    "tool": tool,
                    "model": actual_model,
                    "input_tokens": total_input,
                    "output_tokens": output_t or 0,
                    "total_tokens": total_t,
                    "input_cached": i_cached,
                    "input_uncached": i_uncached,
                    "latency_ms": latency_ms or 0,
                    "session_id": sess_id or "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] heatmap_detail cc-switch 出错: {e}")

    # --- Hermes ---
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            cursor.execute("""
                SELECT started_at, model, input_tokens, output_tokens, cache_read_tokens
                FROM sessions
                WHERE started_at >= ?
                ORDER BY started_at ASC
            """, (start_timestamp,))
            for started_at, model, input_t, output_t, cache_read_t in cursor.fetchall():
                dt = datetime.datetime.fromtimestamp(started_at)
                if date_start_ts is not None:
                    if started_at < date_start_ts or started_at >= date_end_ts:
                        continue
                elif weekday is not None:
                    if dt.weekday() != weekday or dt.hour != hour:
                        continue

                input_cached = cache_read_t if cache_read_t else 0
                input_uncached = input_t if input_t else 0
                total_input = input_uncached + input_cached
                output_tokens = output_t if output_t else 0
                total_t = total_input + output_tokens

                events.append({
                    "timestamp": started_at,
                    "time": dt.strftime("%m-%d %H:%M:%S"),
                    "tool": "Hermes",
                    "model": normalize_model_name(model) if model else "Unknown",
                    "input_tokens": total_input,
                    "output_tokens": output_tokens,
                    "total_tokens": total_t,
                    "input_cached": input_cached,
                    "input_uncached": input_uncached,
                    "latency_ms": 0,
                    "session_id": "",
                })
            conn.close()
        except Exception as e:
            print(f"[-] heatmap_detail Hermes 出错: {e}")

    # 去重
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
