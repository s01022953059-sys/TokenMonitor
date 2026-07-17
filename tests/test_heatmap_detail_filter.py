import unittest

import server


class HeatmapDetailFilterTests(unittest.TestCase):
    def setUp(self):
        self.snapshot = {
            "sessions": [
                {"time": "07-16 09:00:00", "tool": "Codex", "model": "gpt-5.6-sol", "total_tokens": 100, "input_cached": 40, "latency_ms": 100},
                {"time": "07-16 10:30:00", "tool": "Claude", "model": "claude-sonnet", "total_tokens": 200, "input_cached": 0, "latency_ms": 300},
                {"time": "07-16 11:00:00", "tool": "Codex", "model": "gpt-5.6-sol", "total_tokens": 300, "input_cached": 100, "latency_ms": 500},
            ]
        }

    def test_filter_recomputes_summary_and_pagination(self):
        result = server._paginate_heatmap_detail_snapshot(
            self.snapshot, "2026-07-16", 1, 1, tool="Codex", start_time="09:30", end_time="11:30"
        )
        self.assertEqual(result["total"], 1)
        self.assertEqual(result["total_pages"], 1)
        self.assertEqual(result["summary"]["total_tokens"], 300)
        self.assertEqual(result["summary"]["total_cached"], 100)
        self.assertEqual(result["summary"]["call_count"], 1)

    def test_filter_options_are_based_on_complete_snapshot(self):
        result = server._paginate_heatmap_detail_snapshot(self.snapshot, "2026-07-16", 1, 50)
        self.assertEqual(result["filter_options"]["tools"], ["Claude", "Codex"])
        self.assertEqual(result["filter_options"]["models"], ["claude-sonnet", "gpt-5.6-sol"])


if __name__ == "__main__":
    unittest.main()
