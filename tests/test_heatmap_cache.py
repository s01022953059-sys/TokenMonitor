import json
import os
import tempfile
import time
import unittest
from unittest import mock

import server


def make_heatmap(days=365):
    rows = [
        {"date": f"day-{index:03d}", "tokens": index, "weekday": index % 7, "month": 1}
        for index in range(days)
    ]
    return {"days": rows, "max_value": days - 1, "start_date": rows[0]["date"], "end_date": rows[-1]["date"]}


class HeatmapCacheTests(unittest.TestCase):
    def setUp(self):
        server._heatmap_refreshing = False

    def test_cold_cache_returns_immediately_and_warms_in_background(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "heatmap.json")
            with mock.patch.object(server, "HEATMAP_CACHE_PATH", path), mock.patch.object(
                server, "_start_heatmap_refresh"
            ) as refresh:
                started = time.monotonic()
                annual = server.get_cached_heatmap(365)
                elapsed = time.monotonic() - started
                monthly = server.get_cached_heatmap(30)

            self.assertEqual(len(annual["days"]), 365)
            self.assertEqual(annual["cache_state"], "warming")
            self.assertLess(elapsed, 0.1)
            self.assertEqual(len(monthly["days"]), 30)
            self.assertEqual(monthly["max_value"], 0)
            self.assertEqual(refresh.call_count, 2)

    def test_stale_cache_returns_immediately_and_refreshes_in_background(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "heatmap.json")
            with open(path, "w", encoding="utf-8") as stream:
                json.dump({"saved_at": 0, "data": make_heatmap()}, stream)
            with mock.patch.object(server, "HEATMAP_CACHE_PATH", path), mock.patch.object(
                server, "_start_heatmap_refresh"
            ) as refresh:
                started = time.monotonic()
                result = server.get_cached_heatmap(90)
                elapsed = time.monotonic() - started

            self.assertEqual(len(result["days"]), 90)
            self.assertLess(elapsed, 0.1)
            refresh.assert_called_once()

    def test_worker_writes_complete_snapshot(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "heatmap.json")
            with mock.patch.object(server, "get_heatmap_data", return_value=make_heatmap()):
                server._build_heatmap_snapshot_worker(path)

            with open(path, "r", encoding="utf-8") as stream:
                snapshot = json.load(stream)
        self.assertEqual(len(snapshot["data"]["days"]), server.HEATMAP_CACHE_DAYS)
        self.assertEqual(snapshot["data"]["max_value"], 364)

    def test_annual_refresh_starts_clean_python_worker_without_forking(self):
        with mock.patch.object(server.subprocess, "run") as run:
            server._refresh_heatmap_snapshot()

        command = run.call_args.args[0]
        self.assertEqual(command[0], server.sys.executable)
        self.assertEqual(command[1], os.path.abspath(server.__file__))
        self.assertEqual(command[2], "--heatmap-worker")
        self.assertEqual(command[3], server.HEATMAP_CACHE_PATH)
        self.assertTrue(run.call_args.kwargs["close_fds"] is False)

    def test_recent_detail_prewarm_runs_before_annual_refresh(self):
        today = time.strftime("%Y-%m-%d")
        with mock.patch.object(server, "_refresh_heatmap_detail") as detail, mock.patch.object(
            server, "get_cached_heatmap"
        ) as annual:
            server._prewarm_recent_dashboard_data()

        self.assertEqual(detail.call_count, 2)
        self.assertEqual(detail.call_args_list[0].args[0], today)
        self.assertEqual(detail.call_args_list[0].args[1:], ())
        annual.assert_called_once_with(server.HEATMAP_CACHE_DAYS)


if __name__ == "__main__":
    unittest.main()
