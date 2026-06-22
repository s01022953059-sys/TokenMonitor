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
    """只读扫描 cc-switch 数据库中今天的 API 请求记录，提取包含缓存细节的高精度 Token 消耗"""
    logs_data = []
    
    if not os.path.exists(CC_SWITCH_DB_PATH):
        return logs_data
        
    try:
        conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
        cursor = conn.cursor()
        
        # 筛选今天生成的成功请求日志 (status_code = 200)
        query = """
            SELECT created_at, app_type, model, input_tokens, output_tokens, cache_read_tokens 
            FROM proxy_request_logs 
            WHERE created_at >= ? AND status_code = 200
            ORDER BY created_at ASC
        """
        cursor.execute(query, (today_start,))
        rows = cursor.fetchall()
        conn.close()
        
        for created_at, app_type, model, input_t, output_t, cache_read_t in rows:
            # 格式化时间为本地时间
            local_time = datetime.datetime.fromtimestamp(created_at).strftime("%H:%M:%S")
            
            # 区分缓存命中与未命中 Token (对标官网)
            input_cached = cache_read_t
            input_uncached = max(0, input_t - cache_read_t)
            total_t = input_t + output_t

            logs_data.append({
                "time": local_time,
                "timestamp": created_at,
                "tool": app_type.capitalize() if app_type else "Other",
                "model": normalize_model_name(model),
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

    判定规则: 时间戳在 DEDUP_WINDOW_SECONDS 窗口内 + 同 model + 同 total_tokens
    视为同一事件, 只保留 timestamp 最早的一条。cc-switch / Hermes / Antigravity
    之间偶有重叠 (例如 Hermes 把经过 cc-switch 代理的请求再记一次),
    用这个简单启发式足以消掉大部分重复, 又不会误伤相邻请求。
    返回按 timestamp 升序排列的列表。
    """
    if not events:
        return []
    # 按时间升序, 保证 group key 相同时保留最早的
    ordered = sorted(events, key=lambda x: x["timestamp"])
    seen_keys = set()
    deduped = []
    for ev in ordered:
        bucket = ev["timestamp"] // DEDUP_WINDOW_SECONDS
        key = (bucket, (ev.get("model") or "").lower(), ev.get("total_tokens", 0))
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
    """获取过去 days 天内每天的 Token 消耗数据，支持工具和模型两个维度细分统计"""
    now = datetime.datetime.now()
    start_date = now - datetime.timedelta(days=days - 1)
    start_date_midnight = datetime.datetime(start_date.year, start_date.month, start_date.day, 0, 0, 0)
    start_timestamp = int(start_date_midnight.timestamp())
    
    # 准备近 days 天的日期列表
    date_list = []
    for i in range(days):
        d = start_date_midnight + datetime.timedelta(days=i)
        date_list.append(d.strftime("%Y-%m-%d"))
        
    # 定义标准工具和主流模型分类
    tools = ["Antigravity", "Hermes", "Codex", "Other"]
    models = ["deepseek-v4-flash", "gemini 3.5 flash", "deepseek-v4-pro", "gpt-5.5", "deepseek-v4-flash-free", "Other"]
    
    # 初始化历史数据库结构
    tool_data = {t: {d: 0 for d in date_list} for t in tools}
    model_data = {m: {d: 0 for d in date_list} for m in models}
    daily_totals = {d: 0 for d in date_list}
    
    # 工具与模型归一化映射函数
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

    def get_normalized_model(model_name):
        """长前缀优先匹配, 避免 'gpt-5.4' 抢 'gpt-5.4-mini'。"""
        if not model_name:
            return "Other"
        m_lower = model_name.lower().strip()
        # 把 'Other' 排除掉, 其余按 key 长度倒序, 长的先匹配
        candidates = sorted(
            (m for m in models if m != "Other"),
            key=len,
            reverse=True,
        )
        for m in candidates:
            if m in m_lower:
                return m
        return "Other"
    
    # 1. 扫描 cc-switch 数据库
    if os.path.exists(CC_SWITCH_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            query = """
                SELECT created_at, input_tokens, output_tokens, app_type, model 
                FROM proxy_request_logs 
                WHERE created_at >= ? AND status_code = 200
            """
            cursor.execute(query, (start_timestamp,))
            rows = cursor.fetchall()
            conn.close()
            
            for created_at, input_t, output_t, app_type, model in rows:
                d_str = datetime.datetime.fromtimestamp(created_at).strftime("%Y-%m-%d")
                if d_str in daily_totals:
                    tokens = input_t + output_t
                    daily_totals[d_str] += tokens
                    
                    t_norm = get_normalized_tool(app_type)
                    tool_data[t_norm][d_str] += tokens
                    
                    m_norm = get_normalized_model(model)
                    model_data[m_norm][d_str] += tokens
        except Exception as e:
            print(f"[-] 历史扫描 cc-switch 出错: {e}")
            
    # 2. 扫描 Hermes 数据库
    if os.path.exists(HERMES_DB_PATH):
        try:
            conn = sqlite3.connect(f"file:{HERMES_DB_PATH}?mode=ro", uri=True)
            cursor = conn.cursor()
            query = """
                SELECT started_at, input_tokens, output_tokens, cache_read_tokens, model 
                FROM sessions 
                WHERE started_at >= ?
            """
            cursor.execute(query, (start_timestamp,))
            rows = cursor.fetchall()
            conn.close()
            
            for started_at, input_t, output_t, cache_read_t, model in rows:
                d_str = datetime.datetime.fromtimestamp(started_at).strftime("%Y-%m-%d")
                if d_str in daily_totals:
                    tokens = (input_t or 0) + (cache_read_t or 0) + (output_t or 0)
                    daily_totals[d_str] += tokens
                    
                    tool_data["Hermes"][d_str] += tokens
                    
                    m_norm = get_normalized_model(model)
                    model_data[m_norm][d_str] += tokens
        except Exception as e:
            print(f"[-] 历史扫描 Hermes 出错: {e}")
            
    # 3. 扫描 Antigravity 统计文件
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
