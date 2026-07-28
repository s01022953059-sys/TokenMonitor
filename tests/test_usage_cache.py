import json
import os
import tempfile
import threading
import time
import unittest
from unittest import mock

import server


def usage_snapshot(total=123):
    return {
        "summary": {
            "total_tokens": total,
            "input_tokens": total,
            "output_tokens": 0,
            "input_cached": 0,
            "input_uncached": total,
            "date": time.strftime("%Y-%m-%d"),
            "deepseek_balance": "0.00",
            "deepseek_currency": "CNY",
            "deepseek_status": "Offline",
            "events_after_dedup": 1,
            "events_before_dedup": 1,
        },
        "by_tool": {"Codex": {"total_tokens": total}},
        "by_model": {"test": total},
        "by_model_requests": {"test": 1},
        "by_tool_model": {"Codex": {"test": total}},
        "recent_events": [],
    }


class UsageCacheTests(unittest.TestCase):
    def setUp(self):
        server._usage_refreshing = False
        server._usage_refresh_failed_at = 0.0

    def test_cold_cache_returns_immediately_and_starts_background_refresh(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(
            server, "USAGE_CACHE_PATH", os.path.join(root, "usage.json")
        ), mock.patch.object(server, "_start_usage_refresh", return_value=True) as refresh:
            started = time.monotonic()
            result = server.get_cached_usage()
            elapsed = time.monotonic() - started

        self.assertLess(elapsed, 0.1)
        self.assertEqual(result["cache_state"], "warming")
        self.assertEqual(result["summary"]["total_tokens"], 0)
        self.assertEqual(result["by_tool_model"], {})
        refresh.assert_called_once()

    def test_fresh_cache_returns_without_scanning(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "usage.json")
            with open(path, "w", encoding="utf-8") as stream:
                json.dump({"saved_at": time.time(), "data": usage_snapshot()}, stream)
            with mock.patch.object(server, "USAGE_CACHE_PATH", path), mock.patch.object(
                server, "_start_usage_refresh"
            ) as refresh:
                result = server.get_cached_usage()

        self.assertEqual(result["cache_state"], "ready")
        self.assertEqual(result["summary"]["total_tokens"], 123)
        refresh.assert_not_called()

    def test_stale_cache_is_returned_while_refresh_starts(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "usage.json")
            with open(path, "w", encoding="utf-8") as stream:
                json.dump({"saved_at": 0, "data": usage_snapshot(456)}, stream)
            with mock.patch.object(server, "USAGE_CACHE_PATH", path), mock.patch.object(
                server, "_start_usage_refresh", return_value=True
            ) as refresh:
                result = server.get_cached_usage()

        self.assertEqual(result["cache_state"], "stale")
        self.assertEqual(result["summary"]["total_tokens"], 456)
        refresh.assert_called_once()

    def test_legacy_cache_without_tool_model_breakdown_refreshes_in_background(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "usage.json")
            legacy = usage_snapshot(789)
            legacy.pop("by_tool_model")
            with open(path, "w", encoding="utf-8") as stream:
                json.dump({"saved_at": time.time(), "data": legacy}, stream)
            with mock.patch.object(server, "USAGE_CACHE_PATH", path), mock.patch.object(
                server, "_start_usage_refresh", return_value=True
            ) as refresh:
                result = server.get_cached_usage()

        self.assertEqual(result["summary"]["total_tokens"], 789)
        self.assertEqual(result["by_tool_model"], {})
        self.assertEqual(result["cache_state"], "stale")
        refresh.assert_called_once()

    def test_concurrent_refresh_requests_share_one_scan(self):
        entered = threading.Event()
        release = threading.Event()

        def slow_scan():
            entered.set()
            release.wait(timeout=2)
            return usage_snapshot()

        with tempfile.TemporaryDirectory() as root, mock.patch.object(
            server, "USAGE_CACHE_PATH", os.path.join(root, "usage.json")
        ), mock.patch.object(server, "get_today_usage", side_effect=slow_scan) as scan:
            self.assertTrue(server._start_usage_refresh())
            self.assertTrue(entered.wait(timeout=1))
            self.assertFalse(server._start_usage_refresh())
            release.set()
            deadline = time.monotonic() + 2
            while server._usage_refreshing and time.monotonic() < deadline:
                time.sleep(0.01)

        self.assertFalse(server._usage_refreshing)
        scan.assert_called_once()

    def test_http_server_accepts_bursty_dashboard_polling(self):
        self.assertGreaterEqual(server.ThreadingHTTPServer.request_queue_size, 16)


if __name__ == "__main__":
    unittest.main()
