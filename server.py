#!/usr/bin/env python3
"""Token Monitor 本地仪表盘 HTTP 服务。

设计目标：
* 所有可配置项 (端口 / 更新源 URL) 通过命令行注入，
  Swift 启动器从 Info.plist 读出后透传给本进程，避免在 Python 层再次硬编码。
* / api/app-info     公布版本号与更新源 (UI 静态展示)。
* / api/check-update 真正去拉取 feed URL，比较版本号，返回 ok/latest/error。
  前端调用此接口，About 弹窗里就能看到真实更新状态而非笼统的 "已启用"。
"""

import argparse
import datetime
import hmac
import http.server
import json
import os
import plistlib
import socket
import socketserver
import subprocess
import sys
import threading
import time
try:
    import fcntl  # Unix
except ImportError:
    fcntl = None  # Windows
from urllib import error as urlerror
from urllib import request as urlrequest
from urllib.parse import parse_qs, urlparse

sys.path.append(os.path.dirname(os.path.abspath(__file__)))
try:
    from scanner import get_today_usage, get_historical_usage, get_session_list, get_heatmap_data, get_session_detail, get_heatmap_detail
    from community import get_user_id, is_opted_in, set_optin, report_community_stats, get_community_stats, update_community_profile, get_community_history, create_group, get_group_info, add_group_code, remove_group_code, clear_all_group_codes, get_group_codes
except ImportError:
    from .scanner import get_today_usage, get_historical_usage, get_session_list, get_heatmap_data, get_session_detail, get_heatmap_detail
    from .community import get_user_id, is_opted_in, set_optin, report_community_stats, get_community_stats, update_community_profile, get_community_history, create_group, get_group_info, add_group_code, remove_group_code, clear_all_group_codes, get_group_codes

# 版本号唯一来源: 当前进程所在 Resources 目录的 Info.plist。
# 之所以不走命令行注入, 是因为 start.sh / Swift 启动器只是把端口/更新源
# 透传过来, 版本号属于"应用标识"层级, 让 Python 自己读 plist 避免
# Swift ↔ Python 之间再多一份同步。
#
# 直接执行 server.py 做调试 (不在 .app bundle 内) 时回退到 "0.0-dev",
# 这种情况下前端 About 弹窗会显示 dev 版本, 不会触发误升级提示。
def _read_app_version() -> str:
    candidates = [
        os.path.join(os.path.dirname(os.path.abspath(__file__)), "Info.plist"),
        "/Applications/Token Monitor.app/Contents/Info.plist",
        # 1.3.28 起 silent update 路径, server.py 必须能识别 ~/Applications/ 安装。
        os.path.expanduser("~/Applications/Token Monitor.app/Contents/Info.plist"),
    ]


    for path in candidates:
        if not os.path.exists(path):
            continue
        try:
            with open(path, "rb") as f:
                data = plistlib.load(f)
            version = (data or {}).get("CFBundleShortVersionString")
            if version:
                return str(version).strip()
        except (OSError, ValueError):
            continue
    return "0.0-dev"

# 启动时读一次, 之后每次请求重读 (自更新装新 .app 后, 旧 server.py 进程
# 仍跑, 不重启 — 每次请求重读 Info.plist 保证返回当前 .app 的版本号,
# 不让 About 弹窗显示过期版本)。
APP_VERSION = _read_app_version()
USER_AGENT = f"TokenMonitor/{APP_VERSION} (+https://gitcode.com/baggiopeng/TokenMonitor)"

HEATMAP_CACHE_DAYS = 365
# 只在后台检查是否需要重建快照，避免频繁遍历历史日志。
HEATMAP_CACHE_TTL = 900
COMMUNITY_SYNC_INTERVAL_SECONDS = 5 * 60
USAGE_CACHE_TTL = 30
USAGE_CACHE_PATH = os.environ.get(
    "TOKEN_MONITOR_USAGE_CACHE_FILE",
    os.path.expanduser("~/.token_monitor/usage_cache.json"),
)


def _parse_community_history_days(path):
    """Return one of the four UI-supported ranking-history ranges."""
    raw = parse_qs(urlparse(path).query).get("days", ["30"])[0]
    try:
        days = int(raw)
    except (TypeError, ValueError):
        return 30
    return days if days in {30, 90, 180, 365} else 30


def _parse_community_history_range(path):
    """Return a supported natural calendar period, or empty for legacy days requests."""
    value = str(parse_qs(urlparse(path).query).get("range", [""])[0] or "").strip().lower()
    return value if value in {"week", "month", "quarter", "year"} else ""


HEATMAP_CACHE_PATH = os.environ.get(
    "TOKEN_MONITOR_HEATMAP_CACHE_FILE",
    os.path.expanduser("~/.token_monitor/heatmap_cache.json"),
)
HEATMAP_DETAIL_CACHE_TTL = 5 * 60
HEATMAP_DETAIL_CACHE_PATH = os.environ.get(
    "TOKEN_MONITOR_HEATMAP_DETAIL_CACHE_FILE",
    os.path.expanduser("~/.token_monitor/heatmap_detail_cache.json"),
)
HEATMAP_DETAIL_CACHE_DIR = os.path.join(
    os.path.dirname(HEATMAP_DETAIL_CACHE_PATH), "detail_cache"
)
_heatmap_cache_lock = threading.Lock()
_heatmap_refreshing = False
_heatmap_detail_cache_lock = threading.Lock()
_heatmap_detail_refreshing = {}
_heatmap_detail_failures = {}
_usage_cache_lock = threading.Lock()
_usage_refreshing = False
_usage_refresh_failed_at = 0.0

# ───── 内存级 JSON 缓存 (mtime 失效) ─────
# 避免每次 HTTP 请求都从磁盘全量 json.load, 特别是 3.8MB 的 detail 缓存。
# 仅当文件 mtime 变化时才重新解析, 否则直接返回内存中的对象。

_usage_cache_obj = None
_usage_cache_mtime = 0.0

_heatmap_cache_obj = None
_heatmap_cache_mtime = 0.0


def _read_json_cached(path, cache_obj_ref, cache_mtime_ref):
    """读取 JSON 文件并做内存缓存, 文件 mtime 不变时直接返回缓存对象。

    返回 (parsed_or_None, new_cache_obj, new_mtime)。
    """
    try:
        mtime = os.path.getmtime(path)
    except OSError:
        return None, None, 0.0
    if cache_obj_ref is not None and mtime == cache_mtime_ref:
        return cache_obj_ref, cache_obj_ref, mtime
    try:
        with open(path, "r", encoding="utf-8") as stream:
            parsed = json.load(stream)
        return parsed, parsed, mtime
    except (OSError, ValueError, TypeError):
        return None, None, 0.0


def _empty_usage_snapshot():
    return {
        "summary": {
            "total_tokens": 0,
            "input_tokens": 0,
            "output_tokens": 0,
            "input_cached": 0,
            "input_uncached": 0,
            "date": datetime.date.today().isoformat(),
            "deepseek_balance": "0.00",
            "deepseek_currency": "CNY",
            "deepseek_status": "Offline",
            "events_after_dedup": 0,
            "events_before_dedup": 0,
        },
        "by_tool": {},
        "by_model": {},
        "by_model_requests": {},
        "by_model_input": {},
        "by_model_cached": {},
        "by_tool_model": {},
        "by_tool_model_input": {},
        "by_tool_model_cached": {},
        "by_tool_model_requests": {},
        "recent_events": [],
        "cache_state": "warming",
    }


def _load_usage_snapshot():
    global _usage_cache_obj, _usage_cache_mtime
    parsed, _usage_cache_obj, _usage_cache_mtime = _read_json_cached(
        USAGE_CACHE_PATH, _usage_cache_obj, _usage_cache_mtime
    )
    if parsed is None:
        return None
    data = parsed.get("data")
    if not isinstance(data, dict):
        return None
    if (data.get("summary") or {}).get("date") != datetime.date.today().isoformat():
        return None
    return parsed


def _save_usage_snapshot(data):
    global _usage_cache_obj, _usage_cache_mtime
    directory = os.path.dirname(USAGE_CACHE_PATH)
    if directory:
        os.makedirs(directory, exist_ok=True)
    temporary = USAGE_CACHE_PATH + ".tmp"
    with open(temporary, "w", encoding="utf-8") as stream:
        json.dump({"saved_at": time.time(), "data": data}, stream, ensure_ascii=False)
    os.replace(temporary, USAGE_CACHE_PATH)
    # 写入后同步更新内存缓存, 避免下一次请求重复读磁盘。
    _usage_cache_obj = {"saved_at": time.time(), "data": data}
    try:
        _usage_cache_mtime = os.path.getmtime(USAGE_CACHE_PATH)
    except OSError:
        _usage_cache_mtime = 0.0


def _refresh_usage_snapshot():
    global _usage_refreshing, _usage_refresh_failed_at
    try:
        data = get_today_usage()
        _save_usage_snapshot(data)
        _usage_refresh_failed_at = 0.0
    except Exception as exc:
        _usage_refresh_failed_at = time.time()
        print(f"[-] usage 后台刷新失败: {exc}")
    finally:
        with _usage_cache_lock:
            _usage_refreshing = False


def _start_usage_refresh():
    global _usage_refreshing
    with _usage_cache_lock:
        if _usage_refreshing:
            return False
        if _usage_refresh_failed_at and time.time() - _usage_refresh_failed_at < 30:
            return False
        _usage_refreshing = True
    threading.Thread(target=_refresh_usage_snapshot, daemon=True).start()
    return True


def get_cached_usage():
    """轮询只读取小快照；昂贵扫描在后台单飞执行。"""
    cached = _load_usage_snapshot()
    if cached:
        result = dict(cached["data"])
        schema_outdated = not isinstance(result.get("by_tool_model"), dict)
        result.setdefault("by_tool_model", {})
        # 兼容旧缓存: 补全新增的每模型 input/cached 与每工具 requests 字段
        result.setdefault("by_model_input", {})
        result.setdefault("by_model_cached", {})
        # v1.4.85 新增工具×模型交叉矩阵, 给二级菜单每个子项算指标
        result.setdefault("by_tool_model_input", {})
        result.setdefault("by_tool_model_cached", {})
        result.setdefault("by_tool_model_requests", {})
        if isinstance(result.get("by_tool"), dict):
            for _tool_stats in result["by_tool"].values():
                if isinstance(_tool_stats, dict):
                    _tool_stats.setdefault("requests", 0)
        if schema_outdated or time.time() - float(cached.get("saved_at", 0)) > USAGE_CACHE_TTL:
            result["cache_state"] = "stale"
            _start_usage_refresh()
        else:
            result["cache_state"] = "ready"
        return result

    _start_usage_refresh()
    return _empty_usage_snapshot()


def _slice_heatmap(data, days):
    """从统一的 365 天快照切出指定范围，保证各 Tab 口径一致。"""
    requested = max(1, min(int(days), HEATMAP_CACHE_DAYS))
    rows = list((data or {}).get("days") or [])[-requested:]
    return {
        "days": rows,
        "max_value": max((row.get("tokens", 0) for row in rows), default=0),
        "start_date": rows[0]["date"] if rows else "",
        "end_date": rows[-1]["date"] if rows else "",
    }


def _empty_heatmap(days):
    """首次启动没有快照时也立即给前端完整日期网格，扫描留在后台。"""
    requested = max(1, min(int(days), HEATMAP_CACHE_DAYS))
    now = datetime.datetime.now()
    start = now - datetime.timedelta(days=requested - 1)
    start_midnight = datetime.datetime(start.year, start.month, start.day)
    rows = []
    for offset in range(requested):
        current = start_midnight + datetime.timedelta(days=offset)
        rows.append({
            "date": current.strftime("%Y-%m-%d"),
            "label": current.strftime("%m-%d"),
            "weekday": current.weekday(),
            "month": current.month,
            "tokens": 0,
        })
    return {
        "days": rows,
        "max_value": 0,
        "start_date": rows[0]["date"],
        "end_date": rows[-1]["date"],
        "cache_state": "warming",
    }


def _load_heatmap_snapshot():
    global _heatmap_cache_obj, _heatmap_cache_mtime
    parsed, _heatmap_cache_obj, _heatmap_cache_mtime = _read_json_cached(
        HEATMAP_CACHE_PATH, _heatmap_cache_obj, _heatmap_cache_mtime
    )
    if parsed is None:
        return None
    data = parsed.get("data") or {}
    if len(data.get("days") or []) != HEATMAP_CACHE_DAYS:
        return None
    return parsed


def _save_heatmap_snapshot(data):
    global _heatmap_cache_obj, _heatmap_cache_mtime
    directory = os.path.dirname(HEATMAP_CACHE_PATH)
    if directory:
        os.makedirs(directory, exist_ok=True)
    temporary = HEATMAP_CACHE_PATH + ".tmp"
    with open(temporary, "w", encoding="utf-8") as stream:
        json.dump({"saved_at": time.time(), "data": data}, stream, ensure_ascii=False)
    os.replace(temporary, HEATMAP_CACHE_PATH)
    _heatmap_cache_obj = {"saved_at": time.time(), "data": data}
    try:
        _heatmap_cache_mtime = os.path.getmtime(HEATMAP_CACHE_PATH)
    except OSError:
        _heatmap_cache_mtime = 0.0


def _build_heatmap_snapshot_worker(cache_path):
    """在全新 Python 进程中生成全年快照，避免扫描占住 Web 服务。"""
    try:
        data = get_heatmap_data(HEATMAP_CACHE_DAYS)
        directory = os.path.dirname(cache_path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        temporary = cache_path + ".tmp"
        with open(temporary, "w", encoding="utf-8") as stream:
            json.dump({"saved_at": time.time(), "data": data}, stream, ensure_ascii=False)
        os.replace(temporary, cache_path)
    except OSError:
        pass


def _refresh_heatmap_snapshot():
    global _heatmap_refreshing
    try:
        # 不能从已启动的多线程 Web 服务 fork：macOS 会在子进程打开 SQLite 时崩溃。
        # 直接 exec 一次 server.py，让子进程从干净运行时开始，只执行快照任务。
        subprocess.run(
            [sys.executable, os.path.abspath(__file__), "--heatmap-worker", HEATMAP_CACHE_PATH],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
            close_fds=False,
            timeout=180,
        )
    except (OSError, subprocess.TimeoutExpired):
        pass
    finally:
        with _heatmap_cache_lock:
            _heatmap_refreshing = False


def _start_heatmap_refresh():
    global _heatmap_refreshing
    with _heatmap_cache_lock:
        if _heatmap_refreshing:
            return
        _heatmap_refreshing = True
        threading.Thread(target=_refresh_heatmap_snapshot, daemon=True).start()


def get_cached_heatmap(days):
    """任何请求都立即返回快照；扫描和重建只在后台进行。"""
    global _heatmap_refreshing
    cached = _load_heatmap_snapshot()
    if cached:
        result = _slice_heatmap(cached["data"], days)
        if time.time() - float(cached.get("saved_at", 0)) > HEATMAP_CACHE_TTL:
            result["cache_state"] = "stale"
            _start_heatmap_refresh()
        else:
            result["cache_state"] = "ready"
        return result

    _start_heatmap_refresh()
    return _empty_heatmap(days)


def _empty_heatmap_detail(date, page, page_size):
    return {
        "sessions": [], "total": 0, "page": page, "page_size": page_size,
        "total_pages": 1,
        "summary": {
            "total_tokens": 0, "total_cached": 0, "call_count": 0,
            "avg_latency_ms": 0, "max_latency_ms": 0,
            "peak_tokens": 0, "peak_time": "",
        },
        "cache_state": "warming",
        "date": date,
    }


def _detail_cache_path_for_date(date):
    """按日拆分的 detail 缓存文件路径, 避免全量读写 3.8MB。"""
    return os.path.join(HEATMAP_DETAIL_CACHE_DIR, f"{date}.json")


def _load_heatmap_detail_entry(date):
    """读取单日 detail 缓存, 返回 {"saved_at": float, "data": dict} 或 None。"""
    path = _detail_cache_path_for_date(date)
    try:
        with open(path, "r", encoding="utf-8") as stream:
            entry = json.load(stream)
        if isinstance(entry, dict) and isinstance(entry.get("data"), dict):
            return entry
    except (OSError, ValueError, TypeError):
        pass
    # 兼容旧格式: 从合并文件里读
    return _load_legacy_detail_entry(date)


def _load_legacy_detail_entry(date):
    """从旧的合并格式 heatmap_detail_cache.json 读取单日 (兼容迁移)。"""
    try:
        with open(HEATMAP_DETAIL_CACHE_PATH, "r", encoding="utf-8") as stream:
            cached = json.load(stream)
        entry = cached.get("entries", {}).get(date)
        if entry and isinstance(entry.get("data"), dict):
            return entry
    except (OSError, ValueError, TypeError):
        pass
    return None


def _save_heatmap_detail_entry(date, entry):
    """写入单日 detail 缓存, 只读写单日小文件 (~200KB), 不再全量读写。"""
    directory = HEATMAP_DETAIL_CACHE_DIR
    os.makedirs(directory, exist_ok=True)
    path = _detail_cache_path_for_date(date)
    temporary = path + ".tmp"
    with open(temporary, "w", encoding="utf-8") as stream:
        json.dump(entry, stream, ensure_ascii=False)
    os.replace(temporary, path)


def _migrate_legacy_detail_cache():
    """启动时将旧的合并格式迁移到按日文件, 迁移后删除旧文件。"""
    if not os.path.exists(HEATMAP_DETAIL_CACHE_PATH):
        return
    try:
        with open(HEATMAP_DETAIL_CACHE_PATH, "r", encoding="utf-8") as stream:
            cached = json.load(stream)
        entries = cached.get("entries", {})
        if not isinstance(entries, dict) or not entries:
            return
        os.makedirs(HEATMAP_DETAIL_CACHE_DIR, exist_ok=True)
        for date, entry in entries.items():
            if not isinstance(entry, dict) or not isinstance(entry.get("data"), dict):
                continue
            # 只迁移 weekday:hour 格式的旧 key, 纯日期的也迁移
            path = _detail_cache_path_for_date(date.replace(":", "-"))
            if not os.path.exists(path):
                with open(path, "w", encoding="utf-8") as stream:
                    json.dump(entry, stream, ensure_ascii=False)
        os.remove(HEATMAP_DETAIL_CACHE_PATH)
    except (OSError, ValueError, TypeError):
        pass


def _filter_heatmap_detail_sessions(sessions, tool="", model="", start_time="", end_time=""):
    """在已缓存的日快照上过滤，避免按筛选条件重新扫描日志。"""
    tool = str(tool or "").strip()
    model = str(model or "").strip()
    start_time = str(start_time or "").strip()
    end_time = str(end_time or "").strip()
    filtered = []
    for session in sessions:
        if tool and session.get("tool") != tool:
            continue
        if model and session.get("model") != model:
            continue
        clock = str(session.get("time") or "").split(" ")[-1][:5]
        if start_time and clock < start_time:
            continue
        if end_time and clock > end_time:
            continue
        filtered.append(session)
    return filtered


def _heatmap_detail_filter_options(snapshot):
    sessions = list((snapshot or {}).get("sessions") or [])
    return {
        "tools": sorted({str(s.get("tool")) for s in sessions if s.get("tool")}),
        "models": sorted({str(s.get("model")) for s in sessions if s.get("model")}),
    }


def _paginate_heatmap_detail_snapshot(snapshot, date, page, page_size,
                                      tool="", model="", start_time="", end_time=""):
    """完整日快照只扫描一次，筛选和分页都在内存中完成。"""
    try:
        page = max(1, int(page))
        page_size = max(1, int(page_size))
    except (TypeError, ValueError):
        page, page_size = 1, 50
    sessions = _filter_heatmap_detail_sessions(
        list((snapshot or {}).get("sessions") or []), tool, model, start_time, end_time
    )
    total = len(sessions)
    start = (page - 1) * page_size
    latencies = [s.get("latency_ms", 0) for s in sessions if s.get("latency_ms", 0) > 0]
    peak = max(sessions, key=lambda s: s.get("total_tokens", 0), default=None)
    summary = {
        "total_tokens": sum(s.get("total_tokens", 0) for s in sessions),
        "total_cached": sum(s.get("input_cached", 0) for s in sessions),
        "call_count": total,
        "avg_latency_ms": int(sum(latencies) / len(latencies)) if latencies else 0,
        "max_latency_ms": max(latencies) if latencies else 0,
        "peak_tokens": peak.get("total_tokens", 0) if peak else 0,
        "peak_time": peak.get("time", "") if peak else "",
    }
    return {
        "sessions": sessions[start:start + page_size],
        "total": total,
        "page": page,
        "page_size": page_size,
        "total_pages": max(1, (total + page_size - 1) // page_size),
        "summary": summary,
        "filter_options": _heatmap_detail_filter_options(snapshot),
        "date": date,
    }


def _refresh_heatmap_detail(date):
    try:
        snapshot = get_heatmap_detail(date=date, include_all=True)
        if not isinstance(snapshot, dict):
            raise ValueError("heatmap detail snapshot is invalid")
        with _heatmap_detail_cache_lock:
            _save_heatmap_detail_entry(date, {"saved_at": time.time(), "data": snapshot})
            _heatmap_detail_failures.pop(date, None)
    except Exception as exc:
        # 失败信息只留在本机内存，前端不展示原始路径/数据库错误。
        with _heatmap_detail_cache_lock:
            _heatmap_detail_failures[date] = time.time()
        print(f"[-] heatmap_detail {date} 后台刷新失败: {exc}")
    finally:
        with _heatmap_detail_cache_lock:
            _heatmap_detail_refreshing.pop(date, None)


def _claim_heatmap_detail_refresh(date):
    """同一自然日只允许一个刷新任务，包含启动预热的同步路径。"""
    with _heatmap_detail_cache_lock:
        if date in _heatmap_detail_refreshing:
            return False
        _heatmap_detail_refreshing[date] = time.time()
        return True


def _start_heatmap_detail_refresh(date):
    if not _claim_heatmap_detail_refresh(date):
        return
    threading.Thread(
        target=_refresh_heatmap_detail, args=(date,), daemon=True
    ).start()


def get_cached_heatmap_detail(date, page=1, page_size=50, tool="", model="",
                              start_time="", end_time=""):
    """详情页优先返回本地快照，扫描仅在后台运行。"""
    entry = _load_heatmap_detail_entry(date)
    if entry and isinstance(entry.get("data"), dict):
        result = _paginate_heatmap_detail_snapshot(
            entry["data"], date, page, page_size, tool, model, start_time, end_time
        )
        if time.time() - float(entry.get("saved_at", 0)) > HEATMAP_DETAIL_CACHE_TTL:
            result["cache_state"] = "stale"
            _start_heatmap_detail_refresh(date)
        else:
            result["cache_state"] = "ready"
        return result

    with _heatmap_detail_cache_lock:
        failed_at = _heatmap_detail_failures.get(date)
    if failed_at and time.time() - failed_at < 60:
        result = _empty_heatmap_detail(date, page, page_size)
        result["cache_state"] = "failed"
        return result

    _start_heatmap_detail_refresh(date)
    return _empty_heatmap_detail(date, page, page_size)


def _prewarm_recent_dashboard_data():
    """仅安排今日用量与全年快照，详情由用户打开时按需刷新。"""
    get_cached_usage()
    get_cached_heatmap(HEATMAP_CACHE_DAYS)


def _community_report_loop(stop_event=None, initial_delay=5, interval=None):
    """在后台上报社区统计，不依赖用户是否打开社区页面。

    ``stop_event`` 仅供测试和优雅退出使用；生产服务传入 None，保持守护线程
    生命周期。读取本地快照不会阻塞前台，冷启动扫描完成后由下一个周期上报。
    """
    wait_interval = COMMUNITY_SYNC_INTERVAL_SECONDS if interval is None else interval

    def wait(seconds):
        if stop_event is None:
            time.sleep(seconds)
            return False
        return stop_event.wait(seconds)

    if wait(initial_delay):
        return
    while stop_event is None or not stop_event.is_set():
        try:
            usage = get_cached_usage()
            if usage.get("cache_state") != "warming":
                report_community_stats(usage)
        except Exception:
            # 社区网络或本地数据异常不能影响主服务，下一周期自动重试。
            pass
        if wait(wait_interval):
            return

_parser = argparse.ArgumentParser(add_help=False)
_parser.add_argument("--port", type=int, default=15723)
_parser.add_argument("--update-feed-url", type=str, default="")
_parser.add_argument("--heatmap-worker", type=str, default="")
_args, _ = _parser.parse_known_args()

PORT = _args.port
UPDATE_FEED_URL = (_args.update_feed_url or "").strip()
LOCAL_API_TOKEN = os.environ.get("TOKEN_MONITOR_LOCAL_API_TOKEN", "").strip()
DIRECTORY = os.path.dirname(os.path.abspath(__file__))


def _is_allowed_profile_origin(origin: str, provided_token: str) -> bool:
    """Allow loopback pages and authenticated macOS file:// WebViews."""
    normalized_origin = (origin or "").strip().lower()
    is_webview_local_origin = normalized_origin == "null" or (
        bool(normalized_origin)
        and not normalized_origin.startswith("http://")
        and not normalized_origin.startswith("https://")
    )
    if is_webview_local_origin:
        return bool(LOCAL_API_TOKEN) and hmac.compare_digest(
            (provided_token or "").strip(), LOCAL_API_TOKEN
        )
    if not normalized_origin:
        return True
    return normalized_origin.startswith("http://127.0.0.1:") or normalized_origin.startswith("http://localhost:")

# ----- 单实例锁 -----
# 同一个 Lock 文件 + 文件锁是单实例最稳的真源。
# lock fd 必须在进程生命周期内保持打开, 进程退出时由内核自动释放。
# Unix: fcntl.flock LOCK_EX | LOCK_NB; Windows: msvcrt.locking LK_NBLCK。
import tempfile
SINGLETON_LOCK_PATH = os.environ.get(
    "TOKEN_MONITOR_LOCK_FILE",
    os.path.join(tempfile.gettempdir(), "token_monitor_server.lock"),
)
_singleton_lock_fd = None


def _acquire_singleton_lock() -> bool:
    """非阻塞尝试独占单实例锁。拿到返回 True, 拿不到返回 False。"""
    global _singleton_lock_fd

    # 先检查锁文件里记录的 PID 是否还活着; 如果已死, 删除残留锁文件再重试。
    try:
        with open(SINGLETON_LOCK_PATH, "r") as _stale_fd:
            _stale_pid_str = _stale_fd.read().strip()
            if _stale_pid_str:
                _stale_pid = int(_stale_pid_str)
                try:
                    os.kill(_stale_pid, 0)
                except (OSError, ProcessLookupError):
                    try:
                        os.unlink(SINGLETON_LOCK_PATH)
                    except OSError:
                        pass
    except (FileNotFoundError, ValueError, OSError):
        pass

    try:
        fd = open(SINGLETON_LOCK_PATH, "w")
    except OSError as exc:
        print(f"[server] 无法打开单实例锁文件 {SINGLETON_LOCK_PATH}: {exc}", file=sys.stderr)
        return False
    try:
        if fcntl is not None:
            # Unix: fcntl.flock
            fcntl.flock(fd.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        else:
            # Windows: msvcrt.locking
            import msvcrt
            msvcrt.locking(fd.fileno(), msvcrt.LK_NBLCK, 1)
    except (IOError, OSError):
        fd.close()
        return False
    fd.write(f"{os.getpid()}\\n")
    fd.flush()
    _singleton_lock_fd = fd
    return True




def _normalize_version(value: str) -> str:
    return (value or "").strip().lstrip("vV ").strip()


def _parse_version_tuple(value: str):
    parts = []
    for token in _normalize_version(value).split("."):
        try:
            parts.append(int(token))
        except ValueError:
            try:
                parts.append(int("".join(ch for ch in token if ch.isdigit()) or "0"))
            except ValueError:
                parts.append(0)
    return tuple(parts)


def _compare_versions(latest: str, current: str) -> int:
    """Return 1 if latest > current, 0 if equal, -1 otherwise."""
    a = _parse_version_tuple(latest)
    b = _parse_version_tuple(current)
    length = max(len(a), len(b))
    a += (0,) * (length - len(a))
    b += (0,) * (length - len(b))
    if a == b:
        return 0
    return 1 if a > b else -1


def _normalize_release_download_url(url):
    """GitCode API 会返回不可下载的 api.gitcode.com 附件地址。"""
    value = str(url or "").strip()
    api_prefix = "https://api.gitcode.com/"
    if value.startswith(api_prefix) and "/releases/download/" in value:
        return "https://gitcode.com/" + value[len(api_prefix):]
    return value


def _is_release_attachment(asset):
    """GitCode 源码归档 (type=source) 不是可安装附件: 其 URL 是
    archive/refs/heads/<tag>.zip, 而 GitCode 禁止同名分支+tag, 对 tag 发布
    该地址必然 404/占位页 (v1.5.14 更新失败事故)。GitHub 风格 assets 无 type 字段。"""
    asset_type = str(asset.get("type") or "").strip().lower()
    return asset_type in ("", "attach")


def _pick_asset_url(payload):
    """从 assets/files 数组里挑出安装包下载地址,优先 .dmg/.zip。"""
    asset_list = payload.get("assets") or payload.get("files") or []
    if not isinstance(asset_list, list):
        return ""
    # 两轮扫描: 先找安装包 .dmg, 再退到 .zip; 只挑真实附件, 排除源码归档。
    preferred = None
    for suffix in (".dmg", ".zip"):
        for asset in asset_list:
            if not isinstance(asset, dict):
                continue
            name = (asset.get("name") or "").lower()
            if name.endswith(suffix) and _is_release_attachment(asset):
                preferred = asset
                break
        if preferred:
            break
    if not preferred:
        return ""
    return _normalize_release_download_url(
        preferred.get("browser_download_url")
        or preferred.get("download_url")
        or preferred.get("downloadUrl")
        or preferred.get("url")
        or preferred.get("html_url")
        or ""
    )


def _extract_release_info(payload):
    """Best-effort 解析 release feed JSON,兼容 GitCode/GitHub/自托管多种格式。"""
    if not isinstance(payload, dict):
        return None
    raw_version = payload.get("version") or payload.get("tag_name") or payload.get("tagName") or ""
    version = _normalize_version(raw_version)
    if not version:
        return None
    title = payload.get("title") or payload.get("name") or f"Token Monitor {version}"
    notes = payload.get("notes") or payload.get("body") or ""
    download_url = (
        payload.get("download_url")
        or payload.get("downloadUrl")
        or ""
    )
    # Release 的 html_url 是详情页，不是安装包。优先从 assets 选当前平台安装包。
    if not download_url:
        download_url = _pick_asset_url(payload)
    if not download_url:
        download_url = payload.get("html_url") or payload.get("htmlUrl") or ""
    download_url = _normalize_release_download_url(download_url)
    return {
        "version": version,
        "title": title,
        "notes": notes,
        "download_url": download_url,
    }


def _check_update_remote():
    """请求更新源,返回结构化结果。永远不会抛异常,失败信息封装在返回值里。"""
    result = {
        "ok": False,
        "current_version": _read_app_version(),
        "latest_version": None,
        "update_available": False,
        "feed_url": UPDATE_FEED_URL,
        "http_status": None,
        "error": None,
        "raw_excerpt": None,
        "title": None,
        "notes": None,
        "download_url": None,
    }
    if not UPDATE_FEED_URL:
        result["error"] = "未配置更新源 (Info.plist 缺少 TokenMonitorUpdateFeedURL)"
        return result

    req = urlrequest.Request(
        UPDATE_FEED_URL,
        headers={
            "User-Agent": USER_AGENT,
            "Accept": "application/json, text/plain;q=0.9, */*;q=0.5",
        },
    )

    try:
        with _open_external_request(req, timeout=8) as response:
            result["http_status"] = response.status
            body = response.read(64 * 1024)
    except urlerror.HTTPError as exc:
        result["http_status"] = exc.code
        try:
            body = exc.read(2048)
            result["raw_excerpt"] = body.decode("utf-8", errors="replace")[:512]
        except Exception:
            pass
        result["error"] = f"HTTP {exc.code} {exc.reason}"
        return result
    except urlerror.URLError as exc:
        result["error"] = f"网络错误: {exc.reason}"
        return result
    except socket.timeout:
        result["error"] = "更新源请求超时"
        return result
    except Exception as exc:  # pragma: no cover - defensive
        result["error"] = f"未预期错误: {exc}"
        return result

    try:
        payload = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        result["raw_excerpt"] = body[:512].decode("utf-8", errors="replace")
        result["error"] = "更新源返回的内容不是 JSON"
        return result

    info = _extract_release_info(payload)
    if not info:
        result["raw_excerpt"] = body[:512].decode("utf-8", errors="replace")
        result["error"] = "更新源 JSON 中缺少版本字段"
        return result

    result["latest_version"] = info["version"]
    result["title"] = info["title"]
    result["notes"] = info["notes"]
    result["download_url"] = info["download_url"] or None
    result["update_available"] = _compare_versions(info["version"], _read_app_version()) > 0
    result["ok"] = True
    return result


def _open_external_request(request, timeout):
    """访问更新与社区服务时显式遵循系统和环境代理设置。

    公司内网、VPN 客户端常把 HTTPS 代理配置在系统层；显式创建 opener 可让
    macOS 的系统代理与 HTTP(S)_PROXY/NO_PROXY 配置都参与解析，同时不影响本地 API。
    """
    proxies = urlrequest.getproxies()
    opener = urlrequest.build_opener(urlrequest.ProxyHandler(proxies))
    return opener.open(request, timeout=timeout)


class TokenMonitorHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=DIRECTORY, **kwargs)

    def end_headers(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, X-Token-Monitor-Client")
        self.send_header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
        self.send_header("Pragma", "no-cache")
        self.send_header("Expires", "0")
        super().end_headers()

    def _write_json(self, status_code, payload):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_OPTIONS(self):
        self.send_response(200)
        self.end_headers()

    def do_POST(self):
        # 社区页的静默补报使用 POST；此前仅处理了昵称接口，导致 macOS 补报始终 404。
        if self.path == "/api/community/report":
            try:
                result = report_community_stats(get_cached_usage())
                self._write_json(200, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return
        if self.path == "/api/community/groups/create":
            try:
                payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                result = create_group(payload.get("name", ""))
                self._write_json(200 if result.get("ok") else 400, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return
        if self.path == "/api/community/groups/join":
            try:
                payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                result = add_group_code(payload.get("code", ""))
                status = 200
                if not result.get("ok"):
                    status = 404 if result.get("status") == "group_not_found" else 400
                    if result.get("status") in {"network_error", "relay_unavailable"}:
                        status = 503
                self._write_json(status, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return
        if self.path == "/api/community/groups/leave":
            try:
                payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                result = remove_group_code(payload.get("code", ""))
                self._write_json(200, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return
        if self.path == "/api/community/groups/clear":
            try:
                result = clear_all_group_codes()
                self._write_json(200, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return
        if self.path != "/api/community/profile":
            self._write_json(404, {"ok": False, "status": "not_found", "message": "接口不存在"})
            return
        try:
            origin = self.headers.get("Origin") or ""
            local_token = self.headers.get("X-Token-Monitor-Client") or ""
            if not _is_allowed_profile_origin(origin, local_token):
                self._write_json(403, {"ok": False, "status": "origin_forbidden", "message": "不允许跨站修改昵称"})
                return
            if not (self.headers.get("Content-Type") or "").lower().startswith("application/json"):
                self._write_json(415, {"ok": False, "status": "invalid_content_type", "message": "请求必须使用 JSON"})
                return
            content_length = int(self.headers.get("Content-Length", "0"))
            if content_length <= 0 or content_length > 4096:
                self._write_json(400, {"ok": False, "status": "name_invalid", "message": "昵称请求格式不正确"})
                return
            payload = json.loads(self.rfile.read(content_length))
            if not isinstance(payload, dict) or not isinstance(payload.get("display_name"), str):
                self._write_json(400, {"ok": False, "status": "name_invalid", "message": "昵称请求格式不正确"})
                return
            result = update_community_profile(payload["display_name"])
            self._write_json(200 if result.get("ok") else 400, result)
        except Exception as exc:
            self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})

    def do_GET(self):
        if self.path == "/api/usage":
            try:
                self._write_json(200, get_cached_usage())
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/history"):
            try:
                # v1.3.92: 解析 days query (前端 /api/history?days=7 调用)
                # 注意: BaseHTTPRequestHandler 把 query 算在 self.path 里 (不像
                # 多数 web 框架分 path/query), 所以 urlparse 拿得到
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                days = 30
                if 'days' in qs:
                    try:
                        d = int(qs['days'][0])
                        if 1 <= d <= 365:
                            days = d
                    except (ValueError, IndexError):
                        pass
                self._write_json(200, get_historical_usage(days))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path == "/api/app-info":
            self._write_json(200, {
                "name": "Token Monitor",
                "version": _read_app_version(),
                "update_feed_url": UPDATE_FEED_URL,
                "update_enabled": bool(UPDATE_FEED_URL),
            })
            return
        if self.path == "/api/check-update":
            self._write_json(200, _check_update_remote())
            return
        if self.path.startswith("/api/sessions"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                days = int(qs.get("days", ["1"])[0])
                page = int(qs.get("page", ["1"])[0])
                page_size = int(qs.get("page_size", ["50"])[0])
                self._write_json(200, get_session_list(days, page=page, page_size=page_size))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/heatmap?") or self.path == "/api/heatmap":
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                days = int(qs.get("days", ["30"])[0])
                self._write_json(200, get_cached_heatmap(days))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/heatmap_detail"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                date = qs.get("date", [None])[0]
                weekday_str = qs.get("weekday", [None])[0]
                hour_str = qs.get("hour", [None])[0]
                days = int(qs.get("days", ["30"])[0])
                page = int(qs.get("page", ["1"])[0])
                page_size = int(qs.get("page_size", ["50"])[0])
                tool = qs.get("tool", [""])[0]
                model = qs.get("model", [""])[0]
                start_time = qs.get("start_time", [""])[0]
                end_time = qs.get("end_time", [""])[0]
                weekday = int(weekday_str) if weekday_str is not None else None
                hour = int(hour_str) if hour_str is not None else None
                if date:
                    self._write_json(200, get_cached_heatmap_detail(
                        date, page=page, page_size=page_size, tool=tool, model=model,
                        start_time=start_time, end_time=end_time
                    ))
                else:
                    self._write_json(200, get_heatmap_detail(weekday=weekday, hour=hour, days=days, page=page, page_size=page_size))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/session_detail"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                session_id = qs.get("session_id", [""])[0]
                tool = qs.get("tool", [""])[0]
                timestamp = qs.get("timestamp", [None])[0]
                page = int(qs.get("page", ["1"])[0])
                page_size = int(qs.get("page_size", ["20"])[0])
                self._write_json(200, get_session_detail(session_id, timestamp=timestamp, page=page, page_size=page_size, tool=tool))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        # ─── 社区 Dashboard API ───
        if self.path.startswith("/api/community/groups/"):
            try:
                code = self.path[len("/api/community/groups/"):]
                result = get_group_info(code)
                if result.get("ok"):
                    self._write_json(200, result)
                else:
                    self._write_json(404, result)
            except Exception as exc:
                self._write_json(500, {"ok": False, "status": "error", "message": str(exc)})
            return

        if self.path == "/api/community" or self.path.startswith("/api/community?"):
            try:
                from urllib.parse import urlparse, parse_qs
                query = parse_qs(urlparse(self.path).query)
                force_refresh = query.get("refresh", ["0"])[0].lower() in {"1", "true", "yes"}
                self._write_json(200, get_community_stats(force_refresh=force_refresh))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        if self.path == "/api/community/optin" or self.path.startswith("/api/community/optin?"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                enabled = qs.get("enabled", ["true"])[0].lower() == "true"
                set_optin(enabled)
                # 如果开启 opt-in, 立即上报一次
                report_result = None
                if enabled:
                    try:
                        report_result = report_community_stats(get_cached_usage())
                    except Exception as exc:
                        report_result = {"ok": False, "status": "error", "message": str(exc)}
                self._write_json(200, {
                    "ok": True,
                    "opted_in": is_opted_in(),
                    "user_id": get_user_id(),
                    "report": report_result,
                })
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        if self.path == "/api/community/history" or self.path.startswith("/api/community/history?"):
            try:
                history = get_community_history(
                    days=_parse_community_history_days(self.path),
                    period=_parse_community_history_range(self.path),
                )
                self._write_json(200, history)
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        if self.path == "/api/community/report":
            try:
                result = report_community_stats(get_cached_usage())
                self._write_json(200, result)
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        if self.path in ("", "/"):
            self.path = "/index.html"
        super().do_GET()


class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True
    request_queue_size = 32

    def server_bind(self):
        """跳过 socket.getfqdn() 反向 DNS 查询,避免在受限网络环境下卡 30s。"""
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.socket.bind(self.server_address)
        self.server_address = self.socket.getsockname()
        host, port = self.server_address[:2]
        self.server_name = host or "localhost"
        self.server_port = port


def main():
    if _args.heatmap_worker:
        _build_heatmap_snapshot_worker(_args.heatmap_worker)
        return

    if not _acquire_singleton_lock():
        print(
            f"[server] 已有 Token Monitor 实例在运行 (单实例锁 {SINGLETON_LOCK_PATH} 被占用), 退出本次启动。",
            file=sys.stderr,
        )
        sys.exit(0)
    # 启动时迁移旧的合并格式 detail 缓存到按日文件。
    _migrate_legacy_detail_cache()

    httpd = ThreadingHTTPServer(("127.0.0.1", PORT), TokenMonitorHandler)
    feed_status = UPDATE_FEED_URL or "<not configured>"
    print(f"[+] Token Monitor 仪表盘已启动: http://127.0.0.1:{PORT}")
    print(f"[+] 更新源 (TokenMonitorUpdateFeedURL): {feed_status}")

    # 最近两天详情先完成；全年热力图随后在独立进程扫描，不能拖慢前台。
    threading.Thread(target=_prewarm_recent_dashboard_data, daemon=True).start()

    # 测试服务必须显式关闭真实社区上报，避免临时 HOME 产生线上匿名身份。
    reporting_disabled = os.environ.get("TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT", "").strip().lower()
    if reporting_disabled not in {"1", "true", "yes"}:
        # 社区统计随安装自动上报：启动后 5 秒首次同步，之后每 5 分钟后台同步。
        threading.Thread(target=_community_report_loop, daemon=True).start()

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n[-] 正在关闭 Web 服务器...")
        httpd.server_close()


if __name__ == "__main__":
    main()
