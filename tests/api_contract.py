#!/usr/bin/env python3
"""第二层：Python/macOS 与 Go/Windows 后端共用的黑盒 API 契约测试。"""

import argparse
import datetime as dt
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen


ROOT = pathlib.Path(__file__).resolve().parents[1]


def unused_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class ReleaseFixture(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.endswith("/community-report/v1/groups/12345"):
            body = json.dumps({"ok": True, "code": "12345", "name": "跨平台测试组", "created_by": "User_FIXTURE"}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if "/community-report/v1/groups/" in self.path:
            body = json.dumps({"ok": False, "status": "group_not_found", "message": "组队不存在"}).encode()
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        base = f"http://127.0.0.1:{self.server.server_port}"
        payload = {
            "tag_name": "v99.0.0",
            "name": "Token Monitor v99.0.0",
            "body": "- 独立 worker 扫描\n- 企业 VPN 代理兼容",
            "assets": [
                {"name": "Token Monitor.dmg", "browser_download_url": base + "/Token-Monitor.dmg"},
                {"name": "TokenMonitor-Setup.exe", "browser_download_url": base + "/TokenMonitor-Setup.exe"},
            ],
        }
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass

    def do_POST(self):
        # API contract tests never touch the real VPS; community reports terminate here.
        length = int(self.headers.get("Content-Length", "0"))
        if length:
            self.rfile.read(length)
        body = json.dumps({"ok": True, "status": "synced", "message": "fixture synced"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class Backend:
    def __init__(self, kind, tempdir, feed_url):
        self.kind = kind
        self.port = unused_port()
        self.base = f"http://127.0.0.1:{self.port}"
        self.tempdir = pathlib.Path(tempdir)
        env = os.environ.copy()
        env.update({
            "HOME": str(self.tempdir / "home"),
            "TOKEN_MONITOR_LOCK_FILE": str(self.tempdir / f"{kind}.lock"),
            "TOKEN_MONITOR_HEATMAP_CACHE_FILE": str(self.tempdir / f"{kind}-heatmap.json"),
            "TOKEN_MONITOR_LOCAL_API_TOKEN": "api-contract-token",
            "TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT": "1",
            "TOKEN_MONITOR_COMMUNITY_RELAY_URL": feed_url + "/community-report",
        })
        pathlib.Path(env["HOME"]).mkdir(parents=True, exist_ok=True)
        sessions = pathlib.Path(env["HOME"]) / ".codex" / "sessions" / "2026" / "07" / "14"
        sessions.mkdir(parents=True, exist_ok=True)
        rollout = sessions / "rollout-contract-019f0000-1111-2222-3333-444444444444.jsonl"
        with rollout.open("w", encoding="utf-8") as stream:
            stream.write(json.dumps({
                "type": "event_msg",
                "payload": {"type": "tool_output", "data": "x" * 512_000},
            }) + "\n")
            for index in range(30):
                stream.write(json.dumps({
                    "type": "response_item",
                    "payload": {"role": "user", "content": [{"text": f"message-{index}"}]},
                }) + "\n")
        if kind == "python":
            command = [sys.executable, "server.py", "--port", str(self.port), "--update-feed-url", feed_url]
        else:
            binary = self.tempdir / "token-monitor-api"
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT / "go_build", check=True)
            command = [str(binary), "--server-only", "--port", str(self.port), "--update-feed-url", feed_url]
        self.process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        self.wait_ready()

    def wait_ready(self):
        deadline = time.monotonic() + 15
        last_error = "not started"
        while time.monotonic() < deadline:
            try:
                status, _ = request_json(self.base + "/api/app-info")
                if status == 200:
                    return
            except Exception as exc:
                last_error = str(exc)
            time.sleep(0.1)
        stderr = self.process.stderr.read() if self.process.stderr else ""
        raise RuntimeError(f"{self.kind} backend did not start: {last_error}; {stderr[-1000:]}")

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()


def request_json(url, method="GET", payload=None, headers=None):
    data = json.dumps(payload).encode() if payload is not None else None
    request = Request(url, data=data, method=method, headers=headers or {})
    try:
        with urlopen(request, timeout=15) as response:
            raw = response.read()
            return response.status, json.loads(raw.decode() or "{}")
    except HTTPError as error:
        raw = error.read()
        try:
            body = json.loads(raw.decode() or "{}")
        except json.JSONDecodeError:
            body = {"raw": raw.decode(errors="replace")}
        return error.code, body


class APIContractTests(unittest.TestCase):
    backend = None

    def get(self, path):
        status, body = request_json(self.backend.base + path)
        self.assertEqual(status, 200, f"{path}: {body}")
        return body

    def test_app_info_and_update_asset(self):
        info = self.get("/api/app-info")
        self.assertEqual(info["name"], "Token Monitor")
        self.assertIsInstance(info["version"], str)
        self.assertTrue(info["update_enabled"])

        update = self.get("/api/check-update")
        self.assertTrue(update["ok"], update)
        self.assertTrue(update["update_available"], update)
        self.assertEqual(update["latest_version"], "99.0.0")
        self.assertTrue(update["download_url"].endswith("Token-Monitor.dmg"), update)
        self.assertIn("独立 worker", update["notes"])

    def test_usage_and_history_shapes(self):
        usage = self.get("/api/usage")
        self.assertIn("summary", usage)
        self.assertIn("by_tool", usage)
        self.assertIn("by_model", usage)
        self.assertIn("by_tool_model", usage)
        self.assertIsInstance(usage["by_tool_model"], dict)
        for model_usage in usage["by_tool_model"].values():
            self.assertIsInstance(model_usage, dict)
        self.assertGreaterEqual(usage["summary"].get("total_tokens", 0), 0)

        for days in (1, 7, 30, 365):
            history = self.get(f"/api/history?days={days}")
            self.assertEqual(len(history["labels"]), days)
            self.assertEqual(len(history["values"]), days)
            for series in list(history["by_tool"].values()) + list(history["by_model"].values()):
                self.assertEqual(len(series), days)
                self.assertTrue(all(value >= 0 for value in series))

    def test_heatmap_ranges_are_complete_and_cached(self):
        expected_end = dt.date.today().isoformat()
        for days in (1, 30, 90, 180, 365):
            heatmap = self.get(f"/api/heatmap?days={days}")
            rows = heatmap["days"]
            self.assertEqual(len(rows), days)
            self.assertEqual(heatmap["start_date"], rows[0]["date"])
            self.assertEqual(heatmap["end_date"], expected_end)
            self.assertEqual(heatmap["max_value"], max(row["tokens"] for row in rows))
            dates = [dt.date.fromisoformat(row["date"]) for row in rows]
            self.assertTrue(all((right - left).days == 1 for left, right in zip(dates, dates[1:])))

        started = time.monotonic()
        cached = self.get("/api/heatmap?days=30")
        # 跨平台 API 黑盒测试包含进程调度和首次快照切换，1 秒仍能约束前台不能阻塞扫描。
        self.assertLess(time.monotonic() - started, 1.0)
        self.assertEqual(len(cached["days"]), 30)

    def test_pagination_and_detail_contracts(self):
        sessions = self.get("/api/sessions?days=7&page=1&page_size=1")
        for key in ("sessions", "total", "page", "page_size", "total_pages"):
            self.assertIn(key, sessions)
        self.assertEqual(sessions["page"], 1)
        self.assertEqual(sessions["page_size"], 1)
        self.assertLessEqual(len(sessions["sessions"]), 1)
        self.assertGreaterEqual(sessions["total_pages"], 1)

        heatmap = self.get("/api/heatmap?days=30")
        started = time.monotonic()
        detail = self.get(f"/api/heatmap_detail?date={heatmap['end_date']}&page=1&page_size=1")
        self.assertLess(time.monotonic() - started, 1.0)
        for key in ("sessions", "total", "page", "page_size", "total_pages", "summary"):
            self.assertIn(key, detail)
        self.assertLessEqual(len(detail["sessions"]), 1)
        self.assertEqual(detail["page"], 1)
        self.assertEqual(detail["page_size"], 1)
        self.assertGreaterEqual(detail["total_pages"], 1)

        # macOS 后端会先异步构建日快照；同一天切换每页条数必须直接复用，
        # 不能再触发一次完整日志扫描。
        deadline = time.monotonic() + 3
        while detail.get("cache_state") == "warming" and time.monotonic() < deadline:
            time.sleep(0.05)
            detail = self.get(f"/api/heatmap_detail?date={heatmap['end_date']}&page=1&page_size=1")
        self.assertNotEqual(detail.get("cache_state"), "warming")
        self.assertIn("filter_options", detail)
        self.assertIn("tools", detail["filter_options"])
        self.assertIn("models", detail["filter_options"])
        started = time.monotonic()
        repaged = self.get(f"/api/heatmap_detail?date={heatmap['end_date']}&page=1&page_size=20")
        self.assertLess(time.monotonic() - started, 1.0)
        self.assertNotEqual(repaged.get("cache_state"), "warming")
        self.assertEqual(repaged["page_size"], 20)
        filtered = self.get(
            f"/api/heatmap_detail?date={heatmap['end_date']}&tool=Codex&model=missing-model&start_time=09:00&end_time=18:00&page=1&page_size=20"
        )
        self.assertEqual(filtered["total"], 0)
        self.assertEqual(filtered["summary"]["call_count"], 0)

        session_id = "019f0000-1111-2222-3333-444444444444"
        started = time.monotonic()
        session_detail = self.get(f"/api/session_detail?session_id={session_id}&page=1&page_size=20")
        self.assertLess(time.monotonic() - started, 1.0)
        self.assertEqual(len(session_detail["messages"]), 20)
        self.assertEqual(session_detail["total"], 30)

        started = time.monotonic()
        second_page = self.get(f"/api/session_detail?session_id={session_id}&page=2&page_size=20")
        self.assertLess(time.monotonic() - started, 1.0)
        self.assertEqual(len(second_page["messages"]), 10)

    def test_profile_rejects_invalid_and_cross_origin_requests(self):
        headers = {"Content-Type": "application/json"}
        status, _ = request_json(self.backend.base + "/api/community/profile", method="POST", payload={}, headers=headers)
        self.assertEqual(status, 400)

        headers["Origin"] = "https://attacker.example"
        status, _ = request_json(
            self.backend.base + "/api/community/profile",
            method="POST",
            payload={"display_name": "ContractUser"},
            headers=headers,
        )
        self.assertEqual(status, 403)

        status, _ = request_json(self.backend.base + "/api/does-not-exist")
        self.assertEqual(status, 404)

    def test_community_report_post_is_available_for_existing_members(self):
        status, report = request_json(self.backend.base + "/api/community/report", method="POST", payload={})
        self.assertEqual(status, 200, report)
        self.assertTrue(report.get("ok"), report)
        self.assertEqual(report.get("status"), "synced")

    def test_group_join_contract_validates_and_persists_real_groups(self):
        headers = {"Content-Type": "application/json"}
        status, invalid = request_json(
            self.backend.base + "/api/community/groups/join",
            method="POST", payload={"code": "123"}, headers=headers,
        )
        self.assertEqual(status, 400, invalid)
        self.assertEqual(invalid.get("status"), "invalid_code")

        status, missing = request_json(
            self.backend.base + "/api/community/groups/join",
            method="POST", payload={"code": "00000"}, headers=headers,
        )
        self.assertEqual(status, 404, missing)
        self.assertEqual(missing.get("status"), "group_not_found")

        status, joined = request_json(
            self.backend.base + "/api/community/groups/join",
            method="POST", payload={"code": "12345"}, headers=headers,
        )
        self.assertEqual(status, 200, joined)
        self.assertTrue(joined.get("ok"), joined)
        self.assertEqual(joined.get("name"), "跨平台测试组")

        group_file = self.backend.tempdir / "home" / ".token_monitor" / "group_code.txt"
        names_file = self.backend.tempdir / "home" / ".token_monitor" / "group_names.json"
        self.assertEqual(group_file.read_text().strip(), "12345")
        self.assertEqual(json.loads(names_file.read_text()).get("12345"), "跨平台测试组")

        status, info = request_json(self.backend.base + "/api/community/groups/12345")
        self.assertEqual(status, 200, info)
        self.assertEqual(info.get("name"), "跨平台测试组")

        status, left = request_json(
            self.backend.base + "/api/community/groups/leave",
            method="POST", payload={"code": "12345"}, headers=headers,
        )
        self.assertEqual(status, 200, left)
        self.assertTrue(left.get("ok"), left)
        self.assertEqual(group_file.read_text().strip(), "")

        status, joined = request_json(
            self.backend.base + "/api/community/groups/join",
            method="POST", payload={"code": "12345"}, headers=headers,
        )
        self.assertEqual(status, 200, joined)
        status, cleared = request_json(
            self.backend.base + "/api/community/groups/clear", method="POST", payload={}, headers=headers,
        )
        self.assertEqual(status, 200, cleared)
        self.assertTrue(cleared.get("ok"), cleared)
        self.assertEqual(group_file.read_text().strip(), "")


def run_backend(kind, tempdir, feed_url):
    backend = Backend(kind, tempdir, feed_url)
    APIContractTests.backend = backend
    try:
        suite = unittest.defaultTestLoader.loadTestsFromTestCase(APIContractTests)
        result = unittest.TextTestRunner(verbosity=2).run(suite)
        return result.wasSuccessful()
    finally:
        backend.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--backend", choices=("python", "go", "all"), default="all")
    args = parser.parse_args()
    fixture = ThreadingHTTPServer(("127.0.0.1", 0), ReleaseFixture)
    threading.Thread(target=fixture.serve_forever, daemon=True).start()
    feed_url = f"http://127.0.0.1:{fixture.server_port}/latest"
    try:
        with tempfile.TemporaryDirectory(prefix="token-monitor-api-") as tempdir:
            kinds = ("python", "go") if args.backend == "all" else (args.backend,)
            passed = all(run_backend(kind, tempdir, feed_url) for kind in kinds)
    finally:
        fixture.shutdown()
        fixture.server_close()
    sys.exit(0 if passed else 1)


if __name__ == "__main__":
    main()
