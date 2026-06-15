#!/usr/bin/env python3
import os
import sqlite3
import json
import datetime
import urllib.request
import re
import glob

# 数据源路径
CC_SWITCH_DB_PATH = os.path.expanduser("~/.cc-switch/cc-switch.db")
ANTIGRAVITY_BRAIN_DIR = os.path.expanduser("~/.gemini/antigravity/brain")

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
    """从 cc-switch 中安全提取 DeepSeek 密钥，并请求官网 API 获取账户实时余额"""
    if not os.path.exists(CC_SWITCH_DB_PATH):
        return {"balance": "0.00", "currency": "CNY", "status": "Offline"}
        
    try:
        conn = sqlite3.connect(f"file:{CC_SWITCH_DB_PATH}?mode=ro", uri=True)
        cursor = conn.cursor()
        cursor.execute("SELECT settings_config FROM providers WHERE id='ddsds'")
        row = cursor.fetchone()
        conn.close()
        
        if not row:
            return {"balance": "0.00", "currency": "CNY", "status": "Provider Not Found"}
            
        cfg = json.loads(row[0])
        key = cfg.get("apiKey", cfg.get("api_key"))
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
                "model": model,
                "input_tokens": input_t,
                "output_tokens": output_t,
                "total_tokens": total_t,
                "input_cached": input_cached,
                "input_uncached": input_uncached
            })
    except Exception as e:
        print(f"[-] 扫描 cc-switch 数据库出错: {e}")
        
    return logs_data

def scan_antigravity_tokens(today_start):
    """只读扫描冰茶 AI (Antigravity 本身) 的官方每日统计文件，精确提取今日消耗"""
    tokens_data = []
    stats_path = os.path.expanduser("~/Library/Application Support/BingchaAI/usage_stats.json")
    
    if not os.path.exists(stats_path):
        return tokens_data
        
    try:
        with open(stats_path, 'r', encoding='utf-8') as f:
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
    db_path = os.path.expanduser("~/.hermes/state.db")
    if not os.path.exists(db_path):
        return logs_data
        
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
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

def get_today_usage():
    """汇总今日所有的大模型 Token 消耗情况以及官方余额"""
    today_start = get_today_midnight_timestamp()
    
    # 1. 扫描三个独立的数据源
    cc_logs = scan_cc_switch_logs(today_start)
    antigravity_logs = scan_antigravity_tokens(today_start)
    hermes_logs = scan_hermes_tokens(today_start)
    
    # 2. 合并并按时间戳排序
    all_logs = cc_logs + antigravity_logs + hermes_logs
    all_logs.sort(key=lambda x: x["timestamp"])
    
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
    
    # 估算成本 (结合 cached tokens 便宜的特征进行科学估算)
    # DeepSeek V4 Flash 缓存价格通常是 0.1$/1M，未缓存价格是 1$/1M，输出是 2$/1M
    # 如果是 Gemini 或其它模型，则进行普通估算。
    # 这里按整体大模型账单的加权进行相对精准计算
    estimated_cost = (input_cached * 0.0000001) + (input_uncached * 0.000001) + (output_tokens * 0.000002)
    
    return {
        "summary": {
            "total_tokens": total_tokens,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "input_cached": input_cached,
            "input_uncached": input_uncached,
            "estimated_cost_usd": round(estimated_cost, 4),
            "date": datetime.datetime.now().strftime("%Y-%m-%d"),
            "deepseek_balance": ds_balance.get("balance", "0.00"),
            "deepseek_currency": ds_balance.get("currency", "CNY"),
            "deepseek_status": ds_balance.get("status", "Offline")
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
        if not model_name:
            return "Other"
        m_lower = model_name.lower().strip()
        for m in models:
            if m != "Other" and m in m_lower:
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
    hermes_db = os.path.expanduser("~/.hermes/state.db")
    if os.path.exists(hermes_db):
        try:
            conn = sqlite3.connect(f"file:{hermes_db}?mode=ro", uri=True)
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
    stats_path = os.path.expanduser("~/Library/Application Support/BingchaAI/usage_stats.json")
    if os.path.exists(stats_path):
        try:
            with open(stats_path, 'r', encoding='utf-8') as f:
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
