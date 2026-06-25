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
import http.server
import json
import os
import plistlib
import socket
import socketserver
import sys
try:
    import fcntl  # Unix
except ImportError:
    fcntl = None  # Windows
from urllib import error as urlerror
from urllib import request as urlrequest

sys.path.append(os.path.dirname(os.path.abspath(__file__)))
try:
    from scanner import get_today_usage, get_historical_usage, get_session_list, get_heatmap_data, get_session_detail, get_heatmap_detail
except ImportError:
    from .scanner import get_today_usage, get_historical_usage, get_session_list, get_heatmap_data, get_session_detail, get_heatmap_detail

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

_parser = argparse.ArgumentParser(add_help=False)
_parser.add_argument("--port", type=int, default=15723)
_parser.add_argument("--update-feed-url", type=str, default="")
_args, _ = _parser.parse_known_args()

PORT = _args.port
UPDATE_FEED_URL = (_args.update_feed_url or "").strip()
DIRECTORY = os.path.dirname(os.path.abspath(__file__))

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


def _pick_asset_url(payload):
    """从 assets/files 数组里挑出安装包下载地址,优先 .dmg/.zip。"""
    asset_list = payload.get("assets") or payload.get("files") or []
    if not isinstance(asset_list, list):
        return ""
    # 两轮扫描: 先找安装包 .dmg, 再退到 .zip, 避免误选源码包。
    preferred = None
    for suffix in (".dmg", ".zip"):
        for asset in asset_list:
            if not isinstance(asset, dict):
                continue
            name = (asset.get("name") or "").lower()
            if name.endswith(suffix):
                preferred = asset
                break
        if preferred:
            break
    if preferred is None and asset_list:
        preferred = asset_list[0] if isinstance(asset_list[0], dict) else None
    if not preferred:
        return ""
    return (
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
        or payload.get("html_url")
        or payload.get("htmlUrl")
        or ""
    )
    # GitCode/GitHub 把安装包放在 assets 数组里,顶层没有 download_url 时回退到 assets。
    if not download_url:
        download_url = _pick_asset_url(payload)
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
        with urlrequest.urlopen(req, timeout=8) as response:
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
    result["download_url"] = info["download_url"] or None
    result["update_available"] = _compare_versions(info["version"], _read_app_version()) > 0
    result["ok"] = True
    return result


class TokenMonitorHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=DIRECTORY, **kwargs)

    def end_headers(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
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

    def do_GET(self):
        if self.path == "/api/usage":
            try:
                self._write_json(200, get_today_usage())
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path == "/api/history":
            try:
                self._write_json(200, get_historical_usage())
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
                self._write_json(200, get_heatmap_data(days))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/heatmap_detail"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                weekday = int(qs.get("weekday", ["0"])[0])
                hour = int(qs.get("hour", ["0"])[0])
                days = int(qs.get("days", ["30"])[0])
                page = int(qs.get("page", ["1"])[0])
                page_size = int(qs.get("page_size", ["50"])[0])
                self._write_json(200, get_heatmap_detail(weekday, hour, days, page=page, page_size=page_size))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return
        if self.path.startswith("/api/session_detail"):
            try:
                from urllib.parse import urlparse, parse_qs
                parsed = urlparse(self.path)
                qs = parse_qs(parsed.query)
                session_id = qs.get("session_id", [""])[0]
                timestamp = qs.get("timestamp", [None])[0]
                page = int(qs.get("page", ["1"])[0])
                page_size = int(qs.get("page_size", ["20"])[0])
                self._write_json(200, get_session_detail(session_id, timestamp=timestamp, page=page, page_size=page_size))
            except Exception as exc:
                self._write_json(500, {"error": str(exc)})
            return

        if self.path in ("", "/"):
            self.path = "/index.html"
        super().do_GET()


class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def server_bind(self):
        """跳过 socket.getfqdn() 反向 DNS 查询,避免在受限网络环境下卡 30s。"""
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.socket.bind(self.server_address)
        self.server_address = self.socket.getsockname()
        host, port = self.server_address[:2]
        self.server_name = host or "localhost"
        self.server_port = port


def main():
    if not _acquire_singleton_lock():
        print(
            f"[server] 已有 Token Monitor 实例在运行 (单实例锁 {SINGLETON_LOCK_PATH} 被占用), 退出本次启动。",
            file=sys.stderr,
        )
        sys.exit(0)
    httpd = ThreadingHTTPServer(("127.0.0.1", PORT), TokenMonitorHandler)
    feed_status = UPDATE_FEED_URL or "<not configured>"
    print(f"[+] Token Monitor 仪表盘已启动: http://127.0.0.1:{PORT}")
    print(f"[+] 更新源 (TokenMonitorUpdateFeedURL): {feed_status}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n[-] 正在关闭 Web 服务器...")
        httpd.server_close()


if __name__ == "__main__":
    main()
