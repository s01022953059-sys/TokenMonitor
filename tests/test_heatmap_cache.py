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

    def test_canonical_cache_serves_all_ranges_without_rescan(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "heatmap.json")
            with mock.patch.object(server, "HEATMAP_CACHE_PATH", path), mock.patch.object(
                server, "get_heatmap_data", return_value=make_heatmap()
            ) as scanner:
                annual = server.get_cached_heatmap(365)
                monthly = server.get_cached_heatmap(30)

            self.assertEqual(len(annual["days"]), 365)
            self.assertEqual(len(monthly["days"]), 30)
            self.assertEqual(monthly["max_value"], 364)
            scanner.assert_called_once_with(365)

    def test_stale_cache_returns_immediately_and_refreshes_in_background(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "heatmap.json")
            with open(path, "w", encoding="utf-8") as stream:
                json.dump({"saved_at": 0, "data": make_heatmap()}, stream)
            with mock.patch.object(server, "HEATMAP_CACHE_PATH", path), mock.patch.object(
                server, "get_heatmap_data", side_effect=lambda _days: (time.sleep(0.2), make_heatmap())[1]
            ):
                started = time.monotonic()
                result = server.get_cached_heatmap(90)
                elapsed = time.monotonic() - started
                deadline = time.monotonic() + 2
                while server._heatmap_refreshing and time.monotonic() < deadline:
                    time.sleep(0.02)

            self.assertEqual(len(result["days"]), 90)
            self.assertLess(elapsed, 0.1)


if __name__ == "__main__":
    unittest.main()
