import unittest
from pathlib import Path

from server import _parse_community_history_days


ROOT = Path(__file__).resolve().parents[1]


class RankHistoryAnimationTest(unittest.TestCase):
    def test_history_days_only_accepts_supported_ranges(self):
        cases = {
            "/api/community/history": 30,
            "/api/community/history?days=30": 30,
            "/api/community/history?days=90": 90,
            "/api/community/history?days=180": 180,
            "/api/community/history?days=365": 365,
            "/api/community/history?days=7": 30,
            "/api/community/history?days=invalid": 30,
        }

        for path, expected in cases.items():
            with self.subTest(path=path):
                self.assertEqual(expected, _parse_community_history_days(path))

    def test_frontends_use_date_rank_line_chart_with_top_ten_limit(self):
        mac_html = (ROOT / "index.html").read_text(encoding="utf-8")
        windows_html = (ROOT / "go_build" / "static" / "index.html").read_text(
            encoding="utf-8"
        )
        self.assertEqual(mac_html, windows_html)

        modal = mac_html.split("<!-- 排名变化动画弹窗 -->", 1)[1].split(
            "<!-- 对话内容查看 -->", 1
        )[0]
        script = mac_html.split("// ===== 排名变化动画 =====", 1)[1].split(
            "// ===== 热力图下钻 =====", 1
        )[0]

        for days, label in ((30, "近 30 天"), (90, "近 90 天"), (180, "近半年"), (365, "近一年")):
            self.assertIn(f'data-days="{days}"', modal)
            self.assertIn(label, modal)

        self.assertIn('role="tablist"', modal)
        self.assertIn('id="rankHistoryChart"', modal)
        self.assertIn("/api/community/history?days=", script)
        self.assertIn("new Chart", script)
        self.assertIn("rankHistoryEndLabels", script)
        self.assertIn("layoutRankHistoryEndLabels", script)
        self.assertIn("RANK_HISTORY_CACHE_TTL", script)
        self.assertIn("_rankHistory.cache", script)
        self.assertIn("participant_count", script)
        self.assertIn("series.slice(0, 10)", script)
        self.assertIn('class="rank-history-icon"', mac_html)
        self.assertNotIn("📊", mac_html)
        self.assertIn("prefers-reduced-motion: reduce", mac_html)
        self.assertNotIn("startRankHistoryPlayback", script)
        self.assertNotIn("rank-history-bar", script)
        self.assertNotIn('id="rankPlayBtn"', modal)
        self.assertNotIn('id="rankPrevBtn"', modal)
        self.assertNotIn('id="rankNextBtn"', modal)


if __name__ == "__main__":
    unittest.main()
