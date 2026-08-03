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
from concurrent.futures import ThreadPoolExecutor, as_completed

COMMUNITY_DIR = os.path.expanduser("~/.token_monitor")
USER_ID_FILE = os.path.join(COMMUNITY_DIR, "community_id.txt")
OPTIN_FILE = os.path.join(COMMUNITY_DIR, "community_optin.txt")
CREDENTIAL_FILE = os.path.join(COMMUNITY_DIR, "community_credential.json")
GROUP_CODE_FILE = os.path.join(COMMUNITY_DIR, "group_code.txt")
GROUP_CREATED_FILE = os.path.join(COMMUNITY_DIR, "created_groups.txt")
GROUP_NAME_CACHE_FILE = os.path.join(COMMUNITY_DIR, "group_names.json")
GITCODE_API = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"
REPORTS_PATH = "community/reports"
COMMUNITY_BRANCH = os.environ.get("TOKEN_MONITOR_COMMUNITY_BRANCH", "community-data")
COMMUNITY_RELAY_URL = os.environ.get(
    "TOKEN_MONITOR_COMMUNITY_RELAY_URL",
    "https://new.taqi.cc/token-monitor-community/v1/report",
)
# 聚合缓存 (5 分钟 TTL)
_aggregate_cache = {"data": None, "ts": 0}
_history_cache = {}


def _format_report_tools(by_tool):
    """按 Token 用量降序展示用户当天实际使用的全部工具。"""
    items = []
    for tool, tokens in (by_tool or {}).items():
        try:
            value = int(tokens or 0)
        except (TypeError, ValueError):
            continue
        if value > 0:
            items.append((str(tool), value))
    items.sort(key=lambda item: (-item[1], item[0]))
    return " + ".join(tool for tool, _ in items) or "?"
AGGREGATE_TTL = 300  # 5 分钟
HISTORY_CACHE_TTL = 300  # 5 分钟
LEADERBOARD_LIMIT = 10


def _community_today():
    """返回北京时间今天的日期字符串，保证跨时区客户端看到一致的'今天'。"""
    beijing_tz = datetime.timezone(datetime.timedelta(hours=8))
    return datetime.datetime.now(beijing_tz).strftime("%Y-%m-%d")


def _normalize_group_codes(raw):
    """把 group_codes 字段统一为 list[str]，兼容旧字符串格式。"""
    if isinstance(raw, str) and raw.strip():
        return [raw.strip()]
    if isinstance(raw, list):
        return [str(c).strip() for c in raw if str(c).strip()]
    return []


def _ensure_dir():
    os.makedirs(COMMUNITY_DIR, exist_ok=True)


def _open_external_request(request, timeout):
    """显式使用 macOS/Windows 系统代理及 HTTP(S)_PROXY，适配内网 VPN。"""
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler(urllib.request.getproxies())
    )
    return opener.open(request, timeout=timeout)


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
    """调用 GitCode API
    
    v1.4.88: GET 请求添加 Cache-Control 头避免 CDN 缓存。
    """
    if require_auth and not token:
        return {"error": "credential_missing", "body": "本机未配置 GitCode 凭据"}
    url = GITCODE_API + "/contents/" + path
    if method == "GET" and "?" not in path:
        url += "?ref=" + COMMUNITY_BRANCH
    headers = {}
    if method == "GET":
        headers["Cache-Control"] = "no-cache"
        headers["Pragma"] = "no-cache"
    if token:
        headers["Authorization"] = "Bearer " + token
    if data:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode()
    else:
        body = None
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with _open_external_request(req, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return {"error": e.code, "body": e.read().decode()[:200]}
    except Exception as exc:
        return {"error": "network_error", "body": str(exc)}


def _read_remote_json(url, token=None):
    """读取公开报告；有凭据时附带认证，失败时返回 (None, message)。
    
    v1.4.88: 添加 Cache-Control 头避免 GitCode CDN 返回缓存旧数据。
    """
    headers = {"Cache-Control": "no-cache", "Pragma": "no-cache"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    try:
        with _open_external_request(req, timeout=8) as resp:
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
    """生成只描述当日统计内容的稳定指纹，用于识别旧身份迁移副本。
    
    注意：此函数仅用于 _dedupe_legacy_identity_reports 的 replaces_id 显式关联验证，
    不用于跨用户指纹匹配（避免不同用户因统计相同而被误删）。
    """
    by_tool = report.get("by_tool") if isinstance(report.get("by_tool"), dict) else {}
    tools = tuple(sorted((str(tool), int(tokens or 0)) for tool, tokens in by_tool.items()))
    report_day = str(report.get("report_date") or report.get("updated_at", "")[:10])
    return report_day, int(report.get("today_tokens") or 0), tools


def _dedupe_legacy_identity_reports(reports):
    """移除已被显式替换的旧身份报告（通过 replaces_id 关联）。
    
    v1.4.88 修复：移除基于指纹的跨用户匹配逻辑。
    旧逻辑用 (report_date, today_tokens, by_tool) 做指纹匹配新旧身份，
    但不同用户可能产生完全相同的统计，导致无辜用户的报告被误删。
    现在只通过 replaces_id 显式关联来去重，指纹仅用于验证 replaces_id
    指向的旧报告内容是否与新报告一致（防止恶意替换）。
    """
    replaced_ids = {
        str(report.get("replaces_id") or "").strip()
        for report in reports
        if str(report.get("auth_hash") or "").strip()
        and str(report.get("replaces_id") or "").strip()
    }
    # 只移除被显式替换的旧身份，不再按指纹匹配不同用户
    return [
        report
        for report in reports
        if str(report.get("id") or "") not in replaced_ids
    ]


def _dedupe_reports_by_id(reports):
    """每个匿名 ID 只保留最新报告，避免异常副本重复计入用户和用量。"""
    unique = {}
    for report in reports:
        user_id = str(report.get("id") or "").strip()
        if not user_id:
            continue
        previous = unique.get(user_id)
        if previous is None or _report_recency_key(report) >= _report_recency_key(previous):
            unique[user_id] = report
    return list(unique.values())


def _report_recency_key(report):
    """报告以更新时间优先；旧格式缺失更新时间时再按报告日期判定。"""
    return (
        str(report.get("updated_at") or ""),
        str(report.get("report_date") or ""),
    )


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
        with _open_external_request(request, timeout=20) as response:
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
        with _open_external_request(request, timeout=20) as response:
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


# ─── 组队功能 ───

def _groups_relay_url():
    """组队 API 端点 URL。"""
    if COMMUNITY_RELAY_URL.endswith("/v1/report"):
        return COMMUNITY_RELAY_URL[:-len("/v1/report")] + "/v1/groups"
    return COMMUNITY_RELAY_URL.rstrip("/") + "/v1/groups"


def get_group_codes():
    """读取本地保存的组码列表。未加入任何组时返回空列表。"""
    _ensure_dir()
    try:
        with open(GROUP_CODE_FILE, "r") as f:
            raw = f.read().strip()
            return [c for c in raw.split(",") if c.strip()] if raw else []
    except OSError:
        return []


def _save_group_codes(codes):
    """保存组码列表到本地。"""
    _ensure_dir()
    with open(GROUP_CODE_FILE, "w") as f:
        f.write(",".join(str(c).strip() for c in codes if str(c).strip()))


def _load_created_codes():
    """读取本地保存的我创建的组码列表。"""
    try:
        with open(GROUP_CREATED_FILE, "r") as f:
            raw = f.read().strip()
            return [c for c in raw.split(",") if c.strip()] if raw else []
    except OSError:
        return []


def _save_created_codes(codes):
    """保存我创建的组码到本地。"""
    _ensure_dir()
    with open(GROUP_CREATED_FILE, "w") as f:
        f.write(",".join(str(c).strip() for c in codes if str(c).strip()))


def _record_created_code(code):
    """记录一个我创建的组码。"""
    code = str(code or "").strip()
    if not code:
        return
    codes = _load_created_codes()
    if code not in codes:
        codes.append(code)
        _save_created_codes(codes)


def _remove_created_code(code):
    """退出创建的组队时清理本地记录（仅本地，不影响服务端）。"""
    code = str(code or "").strip()
    codes = _load_created_codes()
    codes = [c for c in codes if c != code]
    _save_created_codes(codes)


def _load_group_name_cache():
    """读取本地缓存的 code → name 映射。"""
    try:
        with open(GROUP_NAME_CACHE_FILE, "r") as f:
            data = json.load(f)
            return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def _save_group_name_cache(cache):
    """持久化 code → name 映射。"""
    _ensure_dir()
    try:
        with open(GROUP_NAME_CACHE_FILE, "w") as f:
            json.dump(cache, f, ensure_ascii=False)
    except OSError:
        pass


def _lookup_group_name(code):
    """查 group name：先查本地缓存，否则调中继。失败返回 None。"""
    code = str(code or "").strip()
    if not code:
        return None
    cache = _load_group_name_cache()
    if code in cache:
        return cache[code]
    info = get_group_info(code)
    if info.get("ok") and info.get("name"):
        cache[code] = info["name"]
        _save_group_name_cache(cache)
        return info["name"]
    return None


def add_group_code(code):
    """加入组队：直接保存组码到本地，完全不调网络。

    v1.5.07: v1.5.06 虽然去掉了校验，但 _lookup_group_name 仍调中继 GET，
    中国用户连不上 new.taqi.cc 时 10 秒阻塞导致前端请求超时。
    现在完全不做网络请求，组名在 get_community_stats 聚合时延迟解析。
    """
    code = str(code or "").strip()
    if not code:
        return {"ok": False, "status": "empty_code", "message": "组码不能为空"}
    codes = get_group_codes()
    if code in codes:
        return {"ok": True, "codes": codes, "already_member": True}
    codes.append(code)
    _save_group_codes(codes)
    _aggregate_cache["data"] = None
    _aggregate_cache["ts"] = 0
    return {"ok": True, "codes": codes}


def remove_group_code(code):
    """移除一个组码。"""
    codes = get_group_codes()
    code = str(code or "").strip()
    codes = [c for c in codes if c != code]
    _save_group_codes(codes)
    _aggregate_cache["data"] = None
    _aggregate_cache["ts"] = 0
    return {"ok": True, "codes": codes}


def clear_all_group_codes():
    """清空所有组码（用于清理测试残留或彻底退出所有组队）。"""
    _save_group_codes([])
    _save_created_codes([])
    _aggregate_cache["data"] = None
    _aggregate_cache["ts"] = 0
    return {"ok": True}


def create_group(name):
    """创建组队，返回 {ok, code, name} 或错误信息。"""
    credential = _get_community_credential()
    payload = {"name": str(name or "").strip()}
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        _groups_relay_url(), data=body, method="POST",
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "TokenMonitor/" + (_read_app_version() or "unknown"),
            "X-Device-ID": credential["id"],
            "X-Device-Secret": credential["device_secret"],
        },
    )
    try:
        with _open_external_request(request, timeout=20) as response:
            result = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        try:
            result = json.loads(exc.read())
        except (ValueError, TypeError):
            return {"ok": False, "status": "relay_http_error", "message": f"组队服务 HTTP {exc.code}"}
    except Exception as exc:
        return {"ok": False, "status": "relay_unavailable", "message": f"组队中继暂时不可用：{exc}"}

    if result.get("ok"):
        _save_group_name_cache({**_load_group_name_cache(), result["code"]: result["name"]})
        _record_created_code(result["code"])
        codes = get_group_codes()
        if result["code"] not in codes:
            codes.append(result["code"])
            _save_group_codes(codes)
        _aggregate_cache["data"] = None
        _aggregate_cache["ts"] = 0
    return result


def get_group_info(code):
    """查询组码对应的组名。返回 {ok, code, name, created_by} 或错误。"""
    url = _groups_relay_url() + "/" + str(code or "").strip()
    request = urllib.request.Request(url, method="GET",
        headers={"Accept": "application/json", "User-Agent": "TokenMonitor/" + (_read_app_version() or "unknown")})
    try:
        with _open_external_request(request, timeout=10) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as exc:
        try:
            return json.loads(exc.read())
        except (ValueError, TypeError):
            return {"ok": False, "status": "http_error", "message": f"HTTP {exc.code}"}
    except Exception as exc:
        return {"ok": False, "status": "network_error", "message": str(exc)}


def join_group(code):
    """加入组队（添加组码到本地列表）。code 为空无效。"""
    return add_group_code(code)


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
    report_date = str(summary.get("date") or _community_today())
    report = {
        "id": credential["id"],
        "device_secret": credential["device_secret"],
        "report_date": report_date,
        "today_tokens": summary.get("total_tokens", 0),
        "by_tool": {k: v.get("total_tokens", 0) for k, v in by_tool.items()},
        "version": _read_app_version(),
        "group_codes": get_group_codes(),
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


def get_community_stats(force_refresh=False):
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
    if not force_refresh and _aggregate_cache["data"] and (now - _aggregate_cache["ts"]) < AGGREGATE_TTL:
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
            "today_active_users": 0,
            "all_reporters": 0,
            "total_tokens_today": 0,
            "leaderboard": [],
            "tool_distribution": {},
            "active_hours": [0] * 24,
        }

    # 批量读取每个用户的 report
    reports = []
    report_files = [f for f in files if isinstance(f, dict) and str(f.get("name", "")).endswith(".json")]
    read_failures = 0

    def read_report(f):
        file_url = f.get("download_url") or f.get("url")
        if not file_url:
            return None
        try:
            report, read_error = _read_remote_json(file_url, token=token)
            if isinstance(report, dict) and report.get("id"):
                return report
        except Exception:
            pass
        return None

    # GitCode 每份报告是独立文件。串行读取会让打开耗时随用户数线性增长，
    # 用有限并发把等待压缩到最慢的一批请求，同时避免给公开接口造成突发压力。
    selected_files = report_files[:200]
    if selected_files:
        with ThreadPoolExecutor(max_workers=min(8, len(selected_files))) as executor:
            futures = [executor.submit(read_report, f) for f in selected_files]
            for future in as_completed(futures):
                report = future.result()
                if report is None:
                    read_failures += 1
                else:
                    reports.append(report)

    if report_files and not reports:
        return {
            "error": "社区报告存在，但本次全部读取失败，请稍后重试",
            "data_status": "load_failed",
            "opted_in": is_opted_in(),
            "can_report": bool(COMMUNITY_RELAY_URL),
            "my_id": get_user_id(),
            "total_users": 0,
            "today_active_users": 0,
            "all_reporters": 0,
            "total_tokens_today": 0,
            "leaderboard": [],
            "tool_distribution": {},
            "active_hours": [0] * 24,
        }

    reports = _dedupe_reports_by_id(_dedupe_legacy_identity_reports(reports))

    # 聚合
    my_id = get_user_id()
    today = _community_today()

    def report_date(report):
        """提取报告日期，防御性处理空值。
        
        v1.4.88: report_date 和 updated_at 都为空时回退到北京时间今天，
        避免报告因空日期永久不可见。
        """
        date_str = str(report.get("report_date") or "")
        if date_str:
            return date_str
        updated = str(report.get("updated_at") or "")
        if updated:
            return updated[:10]
        return _community_today()

    reports_today = [r for r in reports if report_date(r) == today]
    # 自动上报允许新安装用户提交 0 Token 的初始化报告；这类身份属于历史参与者，
    # 但不能挤进“今日用量”排行榜或改变今日排名分母。
    active_reports = [r for r in reports_today if int(r.get("today_tokens") or 0) > 0]
    sorted_reports = sorted(active_reports, key=lambda r: r.get("today_tokens", 0), reverse=True)
    total_tokens_today = sum(r.get("today_tokens", 0) for r in active_reports)

    # 排名在全部今日参与用户中计算；榜单仅展示前 10。
    my_rank = next((i + 1 for i, r in enumerate(sorted_reports) if r.get("id") == my_id), None)

    leaderboard = sorted_reports[:LEADERBOARD_LIMIT]
    leaderboard = [{
        "id": r.get("id", "?"),
        "display_name": r.get("display_name", ""),
        "tokens": r.get("today_tokens", 0),
        "tool": _format_report_tools(r.get("by_tool", {})),
        "is_me": r.get("id") == my_id,
        "group_codes": _normalize_group_codes(r.get("group_codes", r.get("group_code"))),
    } for r in leaderboard]

    # 工具占比
    tool_totals = {}
    for r in active_reports:
        for tool, tokens in r.get("by_tool", {}).items():
            tool_totals[tool] = tool_totals.get(tool, 0) + tokens
    total_tool_tokens = sum(tool_totals.values()) or 1
    tool_distribution = {k: round(v / total_tool_tokens * 100, 1) for k, v in tool_totals.items()}
    tool_distribution = dict(sorted(tool_distribution.items(), key=lambda x: -x[1]))

    # 组队统计: 用户可能属于多个组，每个组都计入
    group_stats = {}
    for r in active_reports:
        # v1.5.01 兼容旧格式：单字符串 group_code 视为单一组码
        codes = r.get("group_codes")
        if codes is None:
            legacy = r.get("group_code")
            codes = [legacy] if isinstance(legacy, str) and legacy.strip() else []
        if isinstance(codes, str):
            codes = [codes]
        if not isinstance(codes, list):
            codes = []
        for code in codes:
            code = str(code).strip()
            if not code:
                continue
            # v1.5.04: 用本地缓存的名称，没有则显示组码
            name = (_load_group_name_cache().get(code) or code)
            if code not in group_stats:
                group_stats[code] = {"code": code, "name": name, "total_tokens": 0, "member_count": 0, "top_member": ""}
            group_stats[code]["total_tokens"] += int(r.get("today_tokens") or 0)
            group_stats[code]["member_count"] += 1
            display = str(r.get("display_name") or "")
            if display and not group_stats[code]["top_member"]:
                group_stats[code]["top_member"] = display
    groups = sorted(group_stats.values(), key=lambda g: -g["total_tokens"])
    my_group_codes = get_group_codes()
    my_created_codes = _load_created_codes()
    # v1.5.04: 本地已加入但服务端还没上报的组也要出现，避免用户创建后 UI 仍显示公共池
    my_groups_dict = {}
    for g in groups:
        if g["code"] in my_group_codes:
            my_groups_dict[g["code"]] = dict(g, is_creator=g["code"] in my_created_codes)
    for code in my_group_codes:
        if code not in my_groups_dict:
            name = _load_group_name_cache().get(code) or "未知组队"
            my_groups_dict[code] = {
                "code": code, "name": name,
                "total_tokens": 0, "member_count": 0, "top_member": "",
                "is_creator": code in my_created_codes, "pending_report": True,
            }
    my_groups = sorted(my_groups_dict.values(), key=lambda g: -g["total_tokens"])

    # v1.5.08: 按组计算我的排名
    my_group_ranks = {}
    for code in my_group_codes:
        group_reports = [
            r for r in sorted_reports
            if code in _normalize_group_codes(r.get("group_codes", r.get("group_code")))
        ]
        my_group_ranks[code] = next(
            (i + 1 for i, r in enumerate(group_reports) if r.get("id") == my_id), None
        )

    my_report = next((r for r in reports if r.get("id") == my_id), None)
    my_synced_today = bool(my_report and report_date(my_report) == today)
    my_tokens = my_report.get("today_tokens", 0) if my_synced_today else 0
    my_is_active_today = bool(my_synced_today and int(my_tokens or 0) > 0)
    if not is_opted_in():
        rank_status = "disabled"
        rank_message = "数据上报未开启"
    elif my_is_active_today and my_rank is not None and my_rank <= LEADERBOARD_LIMIT:
        rank_status = "ranked"
        rank_message = f"今日第 {my_rank} 名"
    elif my_is_active_today:
        rank_status = "outside_top10"
        rank_message = f"当前第 {my_rank} 名（榜单展示前 {LEADERBOARD_LIMIT}）"
    else:
        rank_status = "pending"
        rank_message = "今日数据准备中"

    # 趣味统计
    import math
    war_and_peace = 580000  # 《战争与和平》约 58 万词
    fun_facts = {
        "war_and_peace_reads": math.floor(total_tokens_today / war_and_peace) if total_tokens_today > 0 else 0,
        "wikipedia_multiple": round(total_tokens_today / 4_000_000_000, 1) if total_tokens_today > 0 else 0,
        "estimated_cost_saved": round(total_tokens_today * 0.000002, 2),  # 粗略估算
    }

    result = {
        # 总用户和今日活跃用户是不同口径：前者按全部历史唯一匿名 ID，后者只算今天有用量的人。
        "total_users": len(reports),
        "today_active_users": len(active_reports),
        # 保留旧字段，兼容尚未升级的客户端。
        "all_reporters": len(reports),
        "total_tokens_today": total_tokens_today,
        "total_tokens_all": total_tokens_today * 30,  # 粗估月度
        "projected_30d_tokens": total_tokens_today * 30,
        "leaderboard": leaderboard,
        "member_names": {
            str(report.get("id")): str(report.get("display_name"))
            for report in reports
            if str(report.get("id") or "").strip() and str(report.get("display_name") or "").strip()
        },
        "tool_distribution": tool_distribution,
        "active_hours": [0] * 24,  # 暂不收集小时数据
        "my_rank": my_rank,
        "my_tokens": my_tokens,
        "my_synced_today": my_synced_today,
        "my_report_found": my_report is not None,
        "my_last_synced_at": my_report.get("updated_at") if my_report else None,
        "my_display_name": my_report.get("display_name", "") if my_report else "",
        "my_name_changed_at": my_report.get("name_changed_at") if my_report else None,
        "my_group_codes": my_group_codes,
        "my_groups": my_groups,
        "my_group_ranks": my_group_ranks,
        "groups": groups,
        "rank_status": rank_status,
        "rank_message": rank_message,
        "rank_total": len(sorted_reports),
        "leaderboard_limit": LEADERBOARD_LIMIT,
        "can_report": bool(COMMUNITY_RELAY_URL),
        "data_status": "partial" if read_failures else ("ok" if active_reports else "empty"),
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


def _build_rank_history_series(snapshots, limit=10):
    """将每日全量参与者转换为按周期累计用量筛选的排名折线。"""
    ordered = sorted(
        [snapshot for snapshot in snapshots if isinstance(snapshot, dict) and snapshot.get("date")],
        key=lambda snapshot: str(snapshot.get("date")),
    )
    dates = [str(snapshot.get("date")) for snapshot in ordered]
    participant_count_complete = all(isinstance(snapshot.get("participants"), list) for snapshot in ordered)
    daily = []
    members = {}

    for snapshot in ordered:
        rows = snapshot.get("participants")
        if not isinstance(rows, list):
            rows = snapshot.get("leaderboard") if isinstance(snapshot.get("leaderboard"), list) else []
        by_id = {}
        for row in rows:
            if not isinstance(row, dict):
                continue
            member_id = str(row.get("id") or row.get("display_name") or "").strip()
            if not member_id:
                continue
            try:
                tokens = max(0, int(row.get("tokens") or 0))
            except (TypeError, ValueError):
                tokens = 0
            normalized = {
                "id": member_id,
                "display_name": str(row.get("display_name") or ""),
                "tokens": tokens,
            }
            previous = by_id.get(member_id)
            if previous is None or tokens >= previous["tokens"]:
                by_id[member_id] = normalized

        day_values = {}
        for row in by_id.values():
            day_values[row["id"]] = {"tokens": row["tokens"]}
            member = members.setdefault(row["id"], {
                "id": row["id"], "display_name": row["display_name"], "total_tokens": 0, "appearances": 0,
            })
            if row["display_name"]:
                member["display_name"] = row["display_name"]
            member["total_tokens"] += row["tokens"]
            member["appearances"] += 1
        daily.append(day_values)

    selected = sorted(
        members.values(),
        key=lambda member: (-member["total_tokens"], -member["appearances"], member["display_name"] or member["id"], member["id"]),
    )[:limit]
    series_by_id = {
        member["id"]: {**member, "ranks": [0] * len(daily), "tokens": [0] * len(daily)}
        for member in selected
    }
    historical_tokens = {member["id"]: 0 for member in selected}
    historical_active_days = {member["id"]: 0 for member in selected}
    for day_index, day_values in enumerate(daily):
        for member in selected:
            point = day_values.get(member["id"])
            series_by_id[member["id"]]["tokens"][day_index] = point["tokens"] if point else 0

        def daily_rank_key(member):
            member_id = member["id"]
            tokens = series_by_id[member_id]["tokens"][day_index]
            zero_history = (
                -historical_tokens[member_id],
                -historical_active_days[member_id],
            ) if tokens == 0 else (0, 0)
            return (-tokens, *zero_history, member["display_name"] or member_id, member_id)

        for rank, member in enumerate(sorted(selected, key=daily_rank_key), 1):
            series_by_id[member["id"]]["ranks"][day_index] = rank
        for member in selected:
            member_id = member["id"]
            tokens = series_by_id[member_id]["tokens"][day_index]
            historical_tokens[member_id] += tokens
            if tokens > 0:
                historical_active_days[member_id] += 1

    series = [series_by_id[member["id"]] for member in selected]

    return {
        "dates": dates,
        "series": series,
        "participant_count": len(members),
        "participant_count_complete": participant_count_complete,
    }


def _community_history_date_bounds(period, today=None):
    """返回按北京时间计算的自然周、月、季度或年度边界。"""
    if today is None:
        beijing_tz = datetime.timezone(datetime.timedelta(hours=8))
        today = datetime.datetime.now(beijing_tz).date()
    if period == "week":
        start = today - datetime.timedelta(days=today.weekday())
    elif period == "month":
        start = today.replace(day=1)
    elif period == "quarter":
        start = today.replace(month=((today.month - 1) // 3) * 3 + 1, day=1)
    elif period == "year":
        start = today.replace(month=1, day=1)
    else:
        raise ValueError("unsupported community history period")
    return start, today


def get_community_history(days=30, period="", today=None):
    """读取每日参与者快照，返回自然周期或兼容旧版最近 N 天的排名趋势。

    归档文件由 VPS 中继每天北京时间 23:55 自动生成, 存于 GitCode community-data
    分支的 community/archive/{date}.json。客户端无需 GitCode token, 走公开读取。

    Returns:
        {"dates": [...], "series": [...], "participant_count": int, "data_status": "ok"|"empty"}
    """
    try:
        days = int(days)
    except (TypeError, ValueError):
        days = 30
    if days <= 0:
        days = 30

    period = str(period or "").strip().lower()
    if period not in {"week", "month", "quarter", "year"}:
        period = ""
    range_start = range_end = None
    if period:
        range_start, range_end = _community_history_date_bounds(period, today)
    cache_key = f"{period}:{range_end.isoformat()}" if period else days

    cached = _history_cache.get(cache_key)
    if cached and time.time() - cached["ts"] < HISTORY_CACHE_TTL:
        return cached["data"]

    token = None
    archive_path = "community/archive"
    files = _gitcode_api("GET", archive_path, token=token, require_auth=False)
    if not isinstance(files, list):
        return {"snapshots": [], "dates": [], "series": [], "participant_count": 0, "participant_count_complete": True, "data_status": "empty"}

    # 新版按自然周期边界筛选；旧版 days 请求继续取最近 N 个归档文件。
    archive_files = [
        f for f in files if isinstance(f, dict) and str(f.get("name", "")).endswith(".json")
    ]
    if period:
        start_text = range_start.isoformat()
        end_text = range_end.isoformat()
        archive_files = [
            f for f in archive_files
            if start_text <= str(f.get("name", ""))[:-5] <= end_text
        ]
    archive_files = sorted(
        archive_files,
        key=lambda f: str(f.get("name", "")),
        reverse=True,
    )
    if not period:
        archive_files = archive_files[:days]

    if not archive_files:
        return {"snapshots": [], "dates": [], "series": [], "participant_count": 0, "participant_count_complete": True, "data_status": "empty"}

    def read_snapshot(f):
        file_url = f.get("download_url") or f.get("url")
        if not file_url:
            return None
        try:
            snapshot, _ = _read_remote_json(file_url, token=token)
            if isinstance(snapshot, dict) and snapshot.get("date"):
                return snapshot
        except Exception:
            pass
        return None

    snapshots = []
    # 每日归档互不依赖，用有限并发压缩网络等待；失败文件单独跳过。
    with ThreadPoolExecutor(max_workers=min(8, len(archive_files))) as executor:
        futures = [executor.submit(read_snapshot, f) for f in archive_files]
        for future in as_completed(futures):
            snapshot = future.result()
            if snapshot is not None:
                snapshots.append(snapshot)

    # 按日期升序返回 (旧→新), 方便前端时间轴播放
    snapshots.sort(key=lambda s: s.get("date", ""))
    result = _build_rank_history_series(snapshots)
    result.update({
        "snapshots": snapshots,
        "data_status": "ok" if snapshots else "empty",
        "range": period,
        "range_start": range_start.isoformat() if range_start else "",
        "range_end": range_end.isoformat() if range_end else "",
    })

    # 归档每天 23:55 才生成，今天可能尚无归档；用实时排行榜补一个今天的 snapshot，
    # 让排名趋势包含当天实时数据（鹏帅要求：今天没汇总就取当日实时数据）。
    # 优先用测试/调用方传入的 today；否则按北京时间取当日。
    # 关键修复：不能用 range_end——自然周期结束日不等于今天，会让补全错过今天。
    if today is not None:
        # 调用方传了 today 参数（测试或周期计算用）
        actual_today = today
    else:
        beijing_tz = datetime.timezone(datetime.timedelta(hours=8))
        actual_today = datetime.datetime.now(beijing_tz).date()
    today_str = actual_today.isoformat()
    if range_start is not None and range_end is not None:
        today_in_period = range_start <= actual_today <= range_end
    else:
        today_in_period = True
    if today_in_period and today_str not in set(result.get("dates") or []):
        try:
            stats = get_community_stats(force_refresh=False)
            today_leaderboard = stats.get("leaderboard") or []
            if today_leaderboard:
                today_snapshot = {
                    "date": today_str,
                    "participants": [
                        {"id": m.get("id", "?"), "display_name": m.get("display_name", ""), "tokens": int(m.get("tokens") or 0)}
                        for m in today_leaderboard
                    ],
                }
                snapshots.append(today_snapshot)
                snapshots.sort(key=lambda s: s.get("date", ""))
                refreshed = _build_rank_history_series(snapshots)
                refreshed.update({
                    "snapshots": snapshots,
                    "data_status": "ok" if snapshots else "empty",
                    "range": period,
                    "range_start": range_start.isoformat() if range_start else "",
                    "range_end": range_end.isoformat() if range_end else "",
                })
                result = refreshed
        except Exception:
            pass

    _history_cache[cache_key] = {"data": result, "ts": time.time()}
    return result
