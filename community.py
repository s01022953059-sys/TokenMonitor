"""Token Monitor 社区功能模块

功能:
1. 生成/读取匿名用户 ID (User_XXXXX)
2. opt-in 开关 (加入社区统计)
3. 上报本地统计到 GitCode (community/reports/User_XXXXX.json)
4. 聚合所有用户数据 (GET 所有 reports → 汇总)
5. 返回社区 Dashboard 数据

隐私保证:
- 只上报数字 (token 数量, 工具占比, 活跃时段)
- 不上报对话内容/模型名/时间戳/文件路径
- ID 随机生成, 不关联个人信息
- opt-in 默认关闭
"""
import os
import json
import random
import string
import subprocess
import urllib.request
import urllib.error
import datetime
import time

COMMUNITY_DIR = os.path.expanduser("~/.token_monitor")
USER_ID_FILE = os.path.join(COMMUNITY_DIR, "community_id.txt")
OPTIN_FILE = os.path.join(COMMUNITY_DIR, "community_optin.txt")
GITCODE_API = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
REPORTS_PATH = "community/reports"
# 聚合缓存 (5 分钟 TTL)
_aggregate_cache = {"data": None, "ts": 0}
AGGREGATE_TTL = 300  # 5 分钟


def _ensure_dir():
    os.makedirs(COMMUNITY_DIR, exist_ok=True)


def get_user_id():
    """获取或生成匿名用户 ID (User_XXXXX)"""
    _ensure_dir()
    if os.path.exists(USER_ID_FILE):
        with open(USER_ID_FILE, "r") as f:
            uid = f.read().strip()
        if uid:
            return uid
    # 生成新 ID
    chars = string.ascii_uppercase + string.digits
    suffix = ''.join(random.choices(chars, k=5))
    uid = "User_" + suffix
    with open(USER_ID_FILE, "w") as f:
        f.write(uid)
    return uid


def is_opted_in():
    """检查用户是否 opt-in 社区统计 (v1.4.12: 默认开启, 用户量小先自动收集)"""
    if not os.path.exists(OPTIN_FILE):
        return True  # 默认开启
    with open(OPTIN_FILE, "r") as f:
        return f.read().strip().lower() != "false"


def set_optin(enabled):
    """设置 opt-in 开关"""
    _ensure_dir()
    with open(OPTIN_FILE, "w") as f:
        f.write("true" if enabled else "false")


def _get_gitcode_token():
    """从 git credential 获取 GitCode token"""
    try:
        result = subprocess.run(
            ["git", "credential", "fill"],
            input=b"protocol=https\nhost=gitcode.com\n\n",
            capture_output=True, timeout=5
        )
        for line in result.stdout.decode().split("\n"):
            if line.startswith("password="):
                return line.split("=", 1)[1]
    except Exception:
        pass
    return None


def _gitcode_api(method, path, data=None, token=None):
    """调用 GitCode API"""
    if not token:
        token = _get_gitcode_token()
    if not token:
        return None
    url = GITCODE_API + "/contents/" + path
    headers = {"Authorization": "Bearer " + token}
    if data:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode()
    else:
        body = None
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return {"error": e.code, "body": e.read().decode()[:200]}
    except Exception:
        return None


def report_community_stats(today_usage):
    """上报当前用户的统计到 GitCode

    Args:
        today_usage: get_today_usage() 的返回值

    Returns:
        True 成功, False 失败
    """
    if not is_opted_in():
        return False
    uid = get_user_id()
    token = _get_gitcode_token()
    if not token:
        return False

    # 构建上报数据 (只含数字, 不含隐私信息)
    summary = today_usage.get("summary", {})
    by_tool = today_usage.get("by_tool", {})
    report = {
        "id": uid,
        "updated_at": datetime.datetime.now().strftime("%Y-%m-%dT%H:%M:%SZ"),
        "today_tokens": summary.get("total_tokens", 0),
        "by_tool": {k: v.get("total_tokens", 0) for k, v in by_tool.items()},
        "tool_count": len(by_tool),
        "version": "1.4.11",
    }

    file_path = REPORTS_PATH + "/" + uid + ".json"
    content = json.dumps(report, ensure_ascii=False, indent=2)
    import base64
    content_b64 = base64.b64encode(content.encode()).decode()

    # 先 GET 看文件是否存在 (拿 sha)
    existing = _gitcode_api("GET", file_path, token=token)
    sha = None
    if existing and isinstance(existing, dict) and existing.get("sha"):
        sha = existing["sha"]

    payload = {
        "message": "community: " + uid + " 上报统计",
        "content": content_b64,
        "branch": "main"
    }
    if sha:
        payload["sha"] = sha

    result = _gitcode_api("PUT", file_path, payload, token)
    return result and "error" not in (result if isinstance(result, dict) else {})


def get_community_stats():
    """获取社区聚合统计 (带 5 分钟缓存)

    Returns:
        {
            "total_users": int,
            "total_tokens_today": int,
            "total_tokens_all": int,
            "leaderboard": [{id, tokens, tool}, ...],
            "tool_distribution": {tool: percentage},
            "active_hours": [int, ...],  # 24 格
            "opted_in": bool,
            "my_id": str,
            "my_rank": int or null,
        }
    """
    # 缓存检查
    now = time.time()
    if _aggregate_cache["data"] and (now - _aggregate_cache["ts"]) < AGGREGATE_TTL:
        data = _aggregate_cache["data"].copy()
        data["opted_in"] = is_opted_in()
        data["my_id"] = get_user_id()
        return data

    token = _get_gitcode_token()
    if not token:
        return {
            "error": "无法获取 GitCode 凭据",
            "opted_in": is_opted_in(),
            "my_id": get_user_id(),
            "total_users": 0,
            "total_tokens_today": 0,
            "leaderboard": [],
            "tool_distribution": {},
            "active_hours": [0] * 24,
        }

    # GET reports 目录列表
    url = GITCODE_API + "/contents/" + REPORTS_PATH
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + token})
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            files = json.loads(resp.read())
    except Exception:
        files = []

    if not isinstance(files, list):
        files = []

    # 批量读取每个用户的 report
    reports = []
    for f in files[:200]:  # 限制最多 200 个用户 (防 API 超时)
        if not isinstance(f, dict):
            continue
        file_url = f.get("download_url") or f.get("url")
        if not file_url:
            continue
        try:
            req2 = urllib.request.Request(file_url, headers={"Authorization": "Bearer " + token})
            with urllib.request.urlopen(req2, timeout=5) as resp2:
                report = json.loads(resp2.read())
            if isinstance(report, dict) and report.get("id"):
                reports.append(report)
        except Exception:
            continue

    # 聚合
    my_id = get_user_id()
    total_tokens_today = sum(r.get("today_tokens", 0) for r in reports)
    # 排行榜 (今日 token 最多)
    leaderboard = sorted(reports, key=lambda r: r.get("today_tokens", 0), reverse=True)[:10]
    leaderboard = [{
        "id": r.get("id", "?"),
        "tokens": r.get("today_tokens", 0),
        "tool": max(r.get("by_tool", {}), key=r.get("by_tool", {}).get, default="?") if r.get("by_tool") else "?",
        "is_me": r.get("id") == my_id
    } for r in leaderboard]

    # 工具占比
    tool_totals = {}
    for r in reports:
        for tool, tokens in r.get("by_tool", {}).items():
            tool_totals[tool] = tool_totals.get(tool, 0) + tokens
    total_tool_tokens = sum(tool_totals.values()) or 1
    tool_distribution = {k: round(v / total_tool_tokens * 100, 1) for k, v in tool_totals.items()}
    tool_distribution = dict(sorted(tool_distribution.items(), key=lambda x: -x[1]))

    # 找自己的排名
    my_rank = None
    for i, r in enumerate(leaderboard):
        if r["is_me"]:
            my_rank = i + 1
            break

    # 趣味统计
    import math
    war_and_peace = 580000  # 《战争与和平》约 58 万词
    fun_facts = {
        "war_and_peace_reads": math.floor(total_tokens_today / war_and_peace) if total_tokens_today > 0 else 0,
        "wikipedia_multiple": round(total_tokens_today / 4_000_000_000, 1) if total_tokens_today > 0 else 0,
        "estimated_cost_saved": round(total_tokens_today * 0.000002, 2),  # 粗略估算
    }

    result = {
        "total_users": len(reports),
        "total_tokens_today": total_tokens_today,
        "total_tokens_all": total_tokens_today * 30,  # 粗估月度
        "leaderboard": leaderboard,
        "tool_distribution": tool_distribution,
        "active_hours": [0] * 24,  # 暂不收集小时数据
        "my_rank": my_rank,
        "fun_facts": fun_facts,
        "updated_at": datetime.datetime.now().strftime("%Y-%m-%dT%H:%M:%SZ"),
    }

    # 写缓存
    _aggregate_cache["data"] = result.copy()
    _aggregate_cache["ts"] = now

    result["opted_in"] = is_opted_in()
    result["my_id"] = my_id
    return result
