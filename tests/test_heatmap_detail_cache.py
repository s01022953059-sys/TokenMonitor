import datetime as dt
import os
import tempfile
import time
import unittest
from unittest import mock

import server


def make_detail(date, page=1, page_size=50):
    return {
        "sessions": [{"timestamp": 1, "total_tokens": 42}],
        "total": 1,
        "page": page,
        "page_size": page_size,
        "total_pages": 1,
        "summary": {"total_tokens": 42, "call_count": 1},
        "date": date,
    }


class HeatmapDetailCacheTests(unittest.TestCase):
    def setUp(self):
        with server._heatmap_detail_cache_lock:
            server._heatmap_detail_refreshing.clear()

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
        scanner.assert_called_once_with(date=date, page=1, page_size=50)

    def test_different_pages_have_independent_cache_entries(self):
        date = dt.date.today().isoformat()
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "detail.json")
            with mock.patch.object(server, "HEATMAP_DETAIL_CACHE_PATH", path), mock.patch.object(
                server, "get_heatmap_detail", side_effect=make_detail
            ):
                server._refresh_heatmap_detail(date, 1, 50)
                server._refresh_heatmap_detail(date, 2, 20)
                first = server.get_cached_heatmap_detail(date, 1, 50)
                second = server.get_cached_heatmap_detail(date, 2, 20)

        self.assertEqual(first["page_size"], 50)
        self.assertEqual(second["page"], 2)
        self.assertEqual(second["page_size"], 20)
