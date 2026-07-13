import datetime as dt
import os
import tempfile
import time
import unittest
from unittest import mock

import server


def make_detail(date, **_kwargs):
    return {
        "sessions": [{"timestamp": 1, "total_tokens": 42}],
        "summary": {"total_tokens": 42, "call_count": 1},
    }


class HeatmapDetailCacheTests(unittest.TestCase):
    def setUp(self):
        with server._heatmap_detail_cache_lock:
            server._heatmap_detail_refreshing.clear()
            server._heatmap_detail_failures.clear()

    def test_cold_detail_returns_immediately_and_warms_in_background(self):
        date = dt.date.today().isoformat()
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "detail.json")
            with mock.patch.object(server, "HEATMAP_DETAIL_CACHE_PATH", path), mock.patch.object(
                server,
                "get_heatmap_detail",
                side_effect=lambda **kwargs: (time.sleep(0.2), make_detail(**kwargs))[1],
            ) as scanner:
                started = time.monotonic()
                cold = server.get_cached_heatmap_detail(date)
                elapsed = time.monotonic() - started
                deadline = time.monotonic() + 2
                while server._heatmap_detail_refreshing and time.monotonic() < deadline:
                    time.sleep(0.02)
                warm = server.get_cached_heatmap_detail(date)

        self.assertLess(elapsed, 0.1)
        self.assertEqual(cold["cache_state"], "warming")
        self.assertEqual(warm["cache_state"], "ready")
        self.assertEqual(warm["summary"]["total_tokens"], 42)
        scanner.assert_called_once_with(date=date, include_all=True)

    def test_different_page_sizes_reuse_one_daily_snapshot(self):
        date = dt.date.today().isoformat()
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "detail.json")
            with mock.patch.object(server, "HEATMAP_DETAIL_CACHE_PATH", path), mock.patch.object(
                server, "get_heatmap_detail", side_effect=make_detail
            ) as scanner:
                server._refresh_heatmap_detail(date)
                first = server.get_cached_heatmap_detail(date, 1, 50)
                second = server.get_cached_heatmap_detail(date, 1, 20)

        self.assertEqual(first["page_size"], 50)
        self.assertEqual(second["page"], 1)
        self.assertEqual(second["page_size"], 20)
        self.assertEqual(second["cache_state"], "ready")
        scanner.assert_called_once_with(date=date, include_all=True)

    def test_failed_snapshot_does_not_leave_the_client_warming_forever(self):
        date = dt.date.today().isoformat()
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "detail.json")
            with mock.patch.object(server, "HEATMAP_DETAIL_CACHE_PATH", path), mock.patch.object(
                server, "get_heatmap_detail", side_effect=RuntimeError("fixture failure")
            ):
                server.get_cached_heatmap_detail(date)
                deadline = time.monotonic() + 2
                while server._heatmap_detail_refreshing and time.monotonic() < deadline:
                    time.sleep(0.02)
                failed = server.get_cached_heatmap_detail(date)

        self.assertEqual(failed["cache_state"], "failed")
