import unittest
from pathlib import Path

from server import _parse_community_history_days, _parse_community_history_range


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

    def test_history_range_only_accepts_calendar_periods(self):
        cases = {
            "/api/community/history": "",
            "/api/community/history?range=week": "week",
            "/api/community/history?range=month": "month",
            "/api/community/history?range=quarter": "quarter",
            "/api/community/history?range=year": "year",
            "/api/community/history?range=90": "",
        }
        for path, expected in cases.items():
            with self.subTest(path=path):
                self.assertEqual(expected, _parse_community_history_range(path))

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

        for period, label in (("week", "本周"), ("month", "本月"), ("quarter", "本季度"), ("year", "本年度")):
            self.assertIn(f'data-range="{period}"', modal)
            self.assertIn(label, modal)
        for old_label in ("近 30 天", "近 90 天", "近半年", "近一年"):
            self.assertNotIn(old_label, modal)
        self.assertIn('class="tab-btn active" role="tab" aria-selected="true" data-range="week"', modal)

        self.assertIn('role="tablist"', modal)
        self.assertIn('id="rankHistoryChart"', modal)
        self.assertIn('id="rankRangeLeaderboard"', modal)
        self.assertIn('id="rankRangeSummaryMeta"', modal)
        self.assertIn("区间总榜", modal)
        self.assertIn("/api/community/history?range=", script)
        self.assertIn("new Chart", script)
        self.assertIn("rankHistoryEndLabels", script)
        self.assertIn("rankHistoryLatestDataIndex", script)
        self.assertIn("rankHistoryRangeRanking", script)
        self.assertIn("communityMemberNames", mac_html)
        self.assertIn("rankHistoryDisplayName", script)
        self.assertIn("completeRankHistoryRanks", script)
        self.assertIn("renderRankRangeLeaderboard", script)
        self.assertIn("item.total_tokens", script)
        self.assertIn("item.appearances", script)
        self.assertIn("renderRankRangeLeaderboard(rangeRanking, dates.length)", script)
        self.assertIn("const latestDataIndex = rankHistoryLatestDataIndex", script)
        # latestDataIndex 现在用 tokenValues 判断有无数据（data 已改为对数 token，名次移到 rankValues）
        self.assertIn("dataset.tokenValues ? dataset.tokenValues[index] : 0", script)
        self.assertIn("tokens > 0", script)
        self.assertNotIn("dataset.data[latestDataIndex]", script)
        self.assertIn("右侧排名", script)
        self.assertIn("token-monitor-rank-history-v4-", script)
        self.assertIn("layoutRankHistoryEndLabels", script)
        self.assertIn("rankHistoryLabelMinGap", script)
        self.assertIn("rankHistoryLabelBackground", script)
        self.assertNotIn("narrow ? 14 : 16", script)
        self.assertIn("RANK_HISTORY_CACHE_TTL", script)
        self.assertIn("_rankHistory.cache", script)
        self.assertIn("participant_count", script)
        self.assertIn("series.slice(0, 10)", script)
        # y 轴改为对数 token 刻度：折线数据用真实 token 数，零值天断开（对数下 0 无定义）
        self.assertIn("type: 'logarithmic'", script)
        # 零值保留为 0，折线连续不断开（鹏帅要求：没数据默认用 0，不要虚线）
        self.assertIn("spanGaps: true", script)
        self.assertNotIn("spanGaps: false", script)
        self.assertIn("rankValues: completedRanks[index]", script)
        self.assertIn("n > 0 ? n : 1", script)
        self.assertNotIn("Number(v) > 0 ? Number(v) : null", script)
        self.assertNotIn("reverse: true", script)
        self.assertNotIn("stepSize: 1", script)
        self.assertNotIn("第 ' + value + ' 名", script)
        self.assertNotIn("return rank >= 1 && rank <= 10 ? rank : null", script)
        # clip 限制折线越界（顶部留 6px 余量，其余裁切），末端标签在 padding 区不受影响
        self.assertIn("clip: {left: 0, top: 6, right: 0, bottom: 0}", script)
        self.assertNotIn("clip: false", script)
        # padding 收回（姓名统一靠右，不再两列错位）
        self.assertIn("top: 18, right: narrow ? 96 : 120, bottom: 10", script)
        self.assertNotIn("right: narrow ? 128 : 200", script)
        self.assertNotIn("right: narrow ? 108 : 168", script)
        # 姓名统一放线段右边，不再两列错位
        self.assertNotIn("item.column = index % 2", script)
        self.assertNotIn("item.column === 1 ? -columnOffset : 0", script)
        self.assertNotIn("columnOffset", script)
        # gapBridge 用 scales 自算坐标（不依赖 meta.data，解决 null 天虚线没画出）
        self.assertIn("scales.x.getPixelForValue(i)", script)
        self.assertIn("scales.y.getPixelForValue(value)", script)
        # 右栏可拖动分隔条 + CSS 变量宽度
        self.assertIn('id="rankHistoryResizer"', mac_html)
        self.assertIn("rank-history-resizer", mac_html)
        self.assertIn("--rank-range-width", script)
        self.assertIn("initRankHistoryResizer", script)
        self.assertIn("token-monitor-rank-range-width", script)
        self.assertIn("MIN_WIDTH = 220", script)
        self.assertIn("MAX_WIDTH = 420", script)
        # 单栏纵向排列，列宽固定对齐
        self.assertIn("minmax(0, 1fr) minmax(48px, 72px) 64px 48px", mac_html)
        self.assertNotIn("splitAt", script)
        self.assertIn('class="rank-history-icon"', mac_html)
        self.assertNotIn("📊", mac_html)
        self.assertIn("prefers-reduced-motion: reduce", mac_html)
        self.assertIn(".rank-range-summary", mac_html)
        self.assertIn(".rank-range-columns", mac_html)
        self.assertIn(".rank-range-item", mac_html)
        # 区间总榜新增对数柱条（量级差距可视化），用绝对对数刻度（动态上下界）
        self.assertIn(".rank-range-bar", mac_html)
        self.assertIn(".rank-range-bar-fill", mac_html)
        self.assertIn("loLog", script)
        self.assertIn("hiLog", script)
        self.assertIn("logSpan", script)
        self.assertIn("Math.log10(Math.max(1, total)) - loLog", script)
        # 零用量天虚线连接插件（保留断开语义，不画数据点）
        self.assertIn("rankHistoryGapBridge", script)
        self.assertIn("setLineDash([4, 4])", script)
        self.assertNotIn("startRankHistoryPlayback", script)
        # 旧的播放按钮柱条 rank-history-bar 已废弃（注意与新的 rank-range-bar 不同）
        self.assertNotIn("rank-history-bar", script)
        self.assertNotIn('id="rankPlayBtn"', modal)
        self.assertNotIn('id="rankPrevBtn"', modal)
        self.assertNotIn('id="rankNextBtn"', modal)


if __name__ == "__main__":
    unittest.main()
