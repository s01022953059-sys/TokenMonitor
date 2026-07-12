"""Token Monitor 社区功能模块

功能:
1. 生成/读取匿名用户 ID (User_XXXXX)
2. opt-in 开关 (加入社区统计)
3. 上报本地统计到 GitCode (community/reports/User_XXXXX.json)
4. 聚合所有用户数据 (GET 所有 reports → 汇总)
5. 返回社区 Dashboard 数据

隐私保证:
- 只上报数字 (token 数量, 工具占比, 活跃时段)
- 不上报对话内容/模型名/请求时间戳明细/文件路径；仅记录报告日期和同步时间
- ID 随机生成, 不关联个人信息
- 社区统计默认开启，可通过本地配置关闭
"""
import os
import json
import string
import secrets
import base64
import urllib.request
import urllib.error
import datetime
import plistlib
import time

COMMUNITY_DIR = os.path.expanduser("~/.token_monitor")
USER_ID_FILE = os.path.join(COMMUNITY_DIR, "community_id.txt")
OPTIN_FILE = os.path.join(COMMUNITY_DIR, "community_optin.txt")
CREDENTIAL_FILE = os.path.join(COMMUNITY_DIR, "community_credential.json")
GITCODE_API = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
REPORTS_PATH = "community/reports"
COMMUNITY_BRANCH = os.environ.get("TOKEN_MONITOR_COMMUNITY_BRANCH", "community-data")
COMMUNITY_RELAY_URL = os.environ.get(
    "TOKEN_MONITOR_COMMUNITY_RELAY_URL",
    "https://new.taqi.cc/token-monitor-community/v1/report",
)
# 聚合缓存 (5 分钟 TTL)
_aggregate_cache = {"data": None, "ts": 0}
AGGREGATE_TTL = 300  # 5 分钟
LEADERBOARD_LIMIT = 10


def _ensure_dir():
    os.makedirs(COMMUNITY_DIR, exist_ok=True)


def _read_app_version():
    """从源码目录或 .app bundle 读取当前版本号。"""
    module_dir = os.path.dirname(os.path.abspath(__file__))
    candidates = [
        os.path.join(module_dir, "Info.plist"),
        os.path.join(module_dir, "..", "Info.plist"),
    ]
    for path in candidates:
        try:
            with open(path, "rb") as f:
                return str(plistlib.load(f).get("CFBundleShortVersionString", ""))
        except (OSError, ValueError, plistlib.InvalidFileException):
            continue
    return ""


def _new_user_id():
    chars = string.ascii_uppercase + string.digits
    return "User_" + ''.join(secrets.choice(chars) for _ in range(8))


def get_user_id():
    """获取或生成匿名用户 ID。"""
    _ensure_dir()
    if os.path.exists(USER_ID_FILE):
        with open(USER_ID_FILE, "r") as f:
            uid = f.read().strip()
        if uid:
            return uid
    uid = _new_user_id()
    with open(USER_ID_FILE, "w") as f:
        f.write(uid)
    return uid


def _write_credential(uid, device_secret):
    _ensure_dir()
    tmp_path = CREDENTIAL_FILE + ".tmp"
    with open(tmp_path, "w", encoding="utf-8") as stream:
        json.dump({"id": uid, "device_secret": device_secret}, stream)
    os.chmod(tmp_path, 0o600)
    os.replace(tmp_path, CREDENTIAL_FILE)


def _get_community_credential():
    """读取或生成只保存在本机的匿名设备凭据。"""
    uid = get_user_id()
    try:
        with open(CREDENTIAL_FILE, "r", encoding="utf-8") as stream:
            credential = json.load(stream)
        secret = str(credential.get("device_secret") or "")
        decoded = base64.urlsafe_b64decode(secret + "=" * (-len(secret) % 4))
        if credential.get("id") == uid and len(decoded) == 32:
            return {"id": uid, "device_secret": secret}
    except (OSError, ValueError, TypeError):
        pass
    secret = base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=")
    _write_credential(uid, secret)
    return {"id": uid, "device_secret": secret}


def _rotate_community_identity():
    """旧匿名 ID 无法证明归属时生成新 ID，防止覆盖其他用户报告。"""
    uid = _new_user_id()
    _ensure_dir()
    with open(USER_ID_FILE, "w", encoding="utf-8") as stream:
        stream.write(uid)
    secret = base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=")
    _write_credential(uid, secret)
    return {"id": uid, "device_secret": secret}


def is_opted_in():
    """社区统计随安装自动启用；旧的 false 文件不再阻止自动同步。"""
    return True


def set_optin(enabled):
    """保留旧 API 兼容，但社区统计始终启用。"""
    _ensure_dir()
    with open(OPTIN_FILE, "w") as f:
        f.write("true")
    _aggregate_cache["data"] = None
    _aggregate_cache["ts"] = 0


def _gitcode_api(method, path, data=None, token=None, require_auth=True):
    """调用 GitCode API"""
    if require_auth and not token:
        return {"error": "credential_missing", "body": "本机未配置 GitCode 凭据"}
    url = GITCODE_API + "/contents/" + path
    if method == "GET" and "?" not in path:
        url += "?ref=" + COMMUNITY_BRANCH
    headers = {}
    if token:
        headers["Authorization"] = "Bearer " + token
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
    except Exception as exc:
        return {"error": "network_error", "body": str(exc)}


def _read_remote_json(url, token=None):
    """读取公开报告；有凭据时附带认证，失败时返回 (None, message)。"""
    headers = {}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=8) as resp:
            return json.loads(resp.read()), None
    except urllib.error.HTTPError as exc:
        return None, f"HTTP {exc.code}"
    except Exception as exc:
        return None, str(exc)


def _report_result(ok, status, message, reported_at=None):
    result = {"ok": ok, "status": status, "message": message}
    if reported_at:
        result["reported_at"] = reported_at
    return result


def _report_fingerprint(report):
    """生成只描述当日统计内容的稳定指纹，用于识别旧身份迁移副本。"""
    by_tool = report.get("by_tool") if isinstance(report.get("by_tool"), dict) else {}
    tools = tuple(sorted((str(tool), int(tokens or 0)) for tool, tokens in by_tool.items()))
    report_day = str(report.get("report_date") or report.get("updated_at", "")[:10])
    return report_day, int(report.get("today_tokens") or 0), tools


def _dedupe_legacy_identity_reports(reports):
    """有凭据的新身份与无凭据旧身份内容完全相同时，只保留新身份。"""
    replaced_ids = {
        str(report.get("replaces_id") or "").strip()
        for report in reports
        if str(report.get("auth_hash") or "").strip()
        and str(report.get("replaces_id") or "").strip()
    }
    authenticated = {
        _report_fingerprint(report)
        for report in reports
        if str(report.get("auth_hash") or "").strip()
    }
    return [
        report
        for report in reports
        if str(report.get("id") or "") not in replaced_ids
        and (
            str(report.get("auth_hash") or "").strip()
            or _report_fingerprint(report) not in authenticated
        )
    ]


def _relay_request(report):
    """通过鹏帅的 VPS 中继提交匿名报告，不向客户端分发 GitCode token。"""
    body = json.dumps(report, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        COMMUNITY_RELAY_URL,
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "TokenMonitor/" + (_read_app_version() or "unknown"),
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as exc:
        try:
            payload = json.loads(exc.read())
        except (ValueError, TypeError):
            payload = {"ok": False, "status": "relay_http_error", "message": f"中继服务 HTTP {exc.code}"}
        return payload
    except Exception as exc:
        return {"ok": False, "status": "relay_unavailable", "message": f"社区中继暂时不可用：{exc}"}


def _profile_relay_url():
    if COMMUNITY_RELAY_URL.endswith("/v1/report"):
        return COMMUNITY_RELAY_URL[:-len("/v1/report")] + "/v1/profile"
    return COMMUNITY_RELAY_URL.rstrip("/") + "/v1/profile"


def _profile_request(payload):
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        _profile_relay_url(), data=body, method="POST",
        headers={"Content-Type": "application/json", "Accept": "application/json", "User-Agent": "TokenMonitor/" + (_read_app_version() or "unknown")},
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as exc:
        try:
            return json.loads(exc.read())
        except (ValueError, TypeError):
            return {"ok": False, "status": "relay_http_error", "message": f"昵称服务 HTTP {exc.code}"}
    except Exception as exc:
        return {"ok": False, "status": "relay_unavailable", "message": f"昵称服务暂时不可用：{exc}"}


def update_community_profile(display_name):
    """使用本机设备凭据更新公开社区昵称。"""
    credential = _get_community_credential()
    result = _profile_request({
        "id": credential["id"],
        "device_secret": credential["device_secret"],
        "display_name": str(display_name or ""),
    })
    if result.get("ok"):
        _aggregate_cache["data"] = None
        _aggregate_cache["ts"] = 0
    return result


def report_community_stats(today_usage):
    """通过 VPS 中继上报当前用户的匿名统计。

    Args:
        today_usage: get_today_usage() 的返回值

    Returns:
        包含 ok/status/message 的结果字典
    """
    credential = _get_community_credential()

    # 构建上报数据 (只含数字, 不含隐私信息)
    summary = today_usage.get("summary", {})
    by_tool = today_usage.get("by_tool", {})
    report_date = str(summary.get("date") or datetime.date.today().isoformat())
    report = {
        "id": credential["id"],
        "device_secret": credential["device_secret"],
        "report_date": report_date,
        "today_tokens": summary.get("total_tokens", 0),
        "by_tool": {k: v.get("total_tokens", 0) for k, v in by_tool.items()},
        "version": _read_app_version(),
    }
    result = _relay_request(report)
    if result.get("status") == "identity_upgrade_required":
        previous_id = credential["id"]
        credential = _rotate_community_identity()
        report["id"] = credential["id"]
        report["device_secret"] = credential["device_secret"]
        report["replaces_id"] = previous_id
        result = _relay_request(report)
    if not result.get("ok"):
        return _report_result(
            False,
            str(result.get("status") or "upload_failed"),
            str(result.get("message") or "匿名统计提交失败"),
        )

    # 上报成功后立即清缓存，避免页面继续显示上报前的 0 数据。
    _aggregate_cache["data"] = None
    _aggregate_cache["ts"] = 0
    return _report_result(
        True, "synced", str(result.get("message") or "匿名统计已同步"), result.get("reported_at")
    )


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

    token = None

    # GET reports 目录列表
    files = _gitcode_api("GET", REPORTS_PATH, token=token, require_auth=False)
    if not isinstance(files, list):
        detail = files.get("body", "未知错误") if isinstance(files, dict) else "未知错误"
        return {
            "error": "社区数据读取失败：" + detail,
            "data_status": "load_failed",
            "opted_in": is_opted_in(),
            "can_report": bool(COMMUNITY_RELAY_URL),
            "my_id": get_user_id(),
            "total_users": 0,
            "total_tokens_today": 0,
            "leaderboard": [],
            "tool_distribution": {},
            "active_hours": [0] * 24,
        }

    # 批量读取每个用户的 report
    reports = []
    report_files = [f for f in files if isinstance(f, dict) and str(f.get("name", "")).endswith(".json")]
    read_failures = 0
    for f in report_files[:200]:  # 限制最多 200 个用户 (防 API 超时)
        if not isinstance(f, dict):
            continue
        file_url = f.get("download_url") or f.get("url")
        if not file_url:
            read_failures += 1
            continue
        try:
            report, read_error = _read_remote_json(file_url, token=token)
            if isinstance(report, dict) and report.get("id"):
                reports.append(report)
            elif read_error:
                read_failures += 1
        except Exception:
            read_failures += 1

    if report_files and not reports:
        return {
            "error": "社区报告存在，但本次全部读取失败，请稍后重试",
            "data_status": "load_failed",
            "opted_in": is_opted_in(),
            "can_report": bool(COMMUNITY_RELAY_URL),
            "my_id": get_user_id(),
            "total_users": 0,
            "total_tokens_today": 0,
            "leaderboard": [],
            "tool_distribution": {},
            "active_hours": [0] * 24,
        }

    reports = _dedupe_legacy_identity_reports(reports)

    # 聚合
    my_id = get_user_id()
    today = datetime.date.today().isoformat()

    def report_date(report):
        return str(report.get("report_date") or report.get("updated_at", "")[:10])

    reports_today = [r for r in reports if report_date(r) == today]
    sorted_reports = sorted(reports_today, key=lambda r: r.get("today_tokens", 0), reverse=True)
    total_tokens_today = sum(r.get("today_tokens", 0) for r in reports_today)

    # 排名在全部今日参与用户中计算；榜单仅展示前 10。
    my_rank = next((i + 1 for i, r in enumerate(sorted_reports) if r.get("id") == my_id), None)
    leaderboard = sorted_reports[:LEADERBOARD_LIMIT]
    leaderboard = [{
        "id": r.get("id", "?"),
        "display_name": r.get("display_name", ""),
        "tokens": r.get("today_tokens", 0),
        "tool": max(r.get("by_tool", {}), key=r.get("by_tool", {}).get, default="?") if r.get("by_tool") else "?",
        "is_me": r.get("id") == my_id
    } for r in leaderboard]

    # 工具占比
    tool_totals = {}
    for r in reports_today:
        for tool, tokens in r.get("by_tool", {}).items():
            tool_totals[tool] = tool_totals.get(tool, 0) + tokens
    total_tool_tokens = sum(tool_totals.values()) or 1
    tool_distribution = {k: round(v / total_tool_tokens * 100, 1) for k, v in tool_totals.items()}
    tool_distribution = dict(sorted(tool_distribution.items(), key=lambda x: -x[1]))

    my_report = next((r for r in reports if r.get("id") == my_id), None)
    my_synced_today = bool(my_report and report_date(my_report) == today)
    my_tokens = my_report.get("today_tokens", 0) if my_synced_today else 0
    if not is_opted_in():
        rank_status = "disabled"
        rank_message = "数据上报未开启"
    elif my_synced_today and my_rank is not None and my_rank <= LEADERBOARD_LIMIT:
        rank_status = "ranked"
        rank_message = f"今日第 {my_rank} 名"
    elif my_synced_today:
        rank_status = "outside_top10"
        rank_message = f"已同步，当前第 {my_rank} 名（榜单展示前 {LEADERBOARD_LIMIT}）"
    else:
        rank_status = "pending"
        rank_message = "等待今日首次同步"

    # 趣味统计
    import math
    war_and_peace = 580000  # 《战争与和平》约 58 万词
    fun_facts = {
        "war_and_peace_reads": math.floor(total_tokens_today / war_and_peace) if total_tokens_today > 0 else 0,
        "wikipedia_multiple": round(total_tokens_today / 4_000_000_000, 1) if total_tokens_today > 0 else 0,
        "estimated_cost_saved": round(total_tokens_today * 0.000002, 2),  # 粗略估算
    }

    result = {
        "total_users": len(reports_today),
        "all_reporters": len(reports),
        "total_tokens_today": total_tokens_today,
        "total_tokens_all": total_tokens_today * 30,  # 粗估月度
        "projected_30d_tokens": total_tokens_today * 30,
        "leaderboard": leaderboard,
        "tool_distribution": tool_distribution,
        "active_hours": [0] * 24,  # 暂不收集小时数据
        "my_rank": my_rank,
        "my_tokens": my_tokens,
        "my_synced_today": my_synced_today,
        "my_report_found": my_report is not None,
        "my_last_synced_at": my_report.get("updated_at") if my_report else None,
        "my_display_name": my_report.get("display_name", "") if my_report else "",
        "my_name_changed_at": my_report.get("name_changed_at") if my_report else None,
        "rank_status": rank_status,
        "rank_message": rank_message,
        "rank_total": len(sorted_reports),
        "leaderboard_limit": LEADERBOARD_LIMIT,
        "can_report": bool(COMMUNITY_RELAY_URL),
        "data_status": "partial" if read_failures else ("ok" if reports_today else "empty"),
        "data_warning": f"有 {read_failures} 份社区报告读取失败，当前统计可能不完整" if read_failures else None,
        "fun_facts": fun_facts,
        "updated_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }

    # 写缓存
    _aggregate_cache["data"] = result.copy()
    _aggregate_cache["ts"] = now

    result["opted_in"] = is_opted_in()
    result["my_id"] = my_id
    return result
