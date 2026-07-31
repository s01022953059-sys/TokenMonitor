"""排名趋势对数刻度测试。

验证排名趋势图改用对数 token 刻度后，能正确体现量级差距（1亿 vs 1M 的差距
远大于 1M vs 0.5M），覆盖 v1.4.66 引入的对数 y 轴与区间总榜对数柱条。

契约依据：AGENTS.md 第 7 条——改代码必须同步补测试，覆盖修复场景、输入与预期输出。
"""
import math
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def _load_rank_script():
    """读取 index.html 里排名变化动画那段 JS。"""
    html = (ROOT / "index.html").read_text(encoding="utf-8")
    return html.split("// ===== 排名变化动画 =====", 1)[1].split(
        "// ===== 热力图下钻 =====", 1
    )[0]


SEGMENT_THRESHOLD = 1_000_000  # 与前端一致：1M 为分段阈值


def _segment_bar_width(total_tokens, max_total_tokens):
    """复刻前端分段刻度柱条宽度。

    ≥1M（大值区）：线性归一化，占 50%-100%——让 2.4亿 vs 70M 差距明显。
    <1M（小值区）：对数归一化，占 0%-50%——让小用户仍可见。
    零用量返回 0（不画柱条）。
    """
    if total_tokens <= 0:
        return 0.0
    seg_log = math.log10(SEGMENT_THRESHOLD)  # 6
    if total_tokens >= SEGMENT_THRESHOLD:
        big_span = max(1, max_total_tokens - SEGMENT_THRESHOLD)
        return 50 + (total_tokens - SEGMENT_THRESHOLD) / big_span * 50
    return math.log10(max(1, total_tokens)) / seg_log * 50


class RankLogScaleContractTest(unittest.TestCase):
    """前端契约：对数刻度的关键代码片段必须存在。"""

    def setUp(self):
        self.script = _load_rank_script()
        self.html = (ROOT / "index.html").read_text(encoding="utf-8")
        self.windows_html = (ROOT / "go_build" / "static" / "index.html").read_text(
            encoding="utf-8"
        )

    def test_frontends_byte_identical(self):
        """macOS 与 Windows 两份 index.html 必须字节一致。"""
        self.assertEqual(self.html, self.windows_html)

    def test_y_axis_is_logarithmic(self):
        """主图 y 轴必须是对数刻度（min:0.1 让 0 值线离开底部不被遮挡），不能残留 linear 名次配置。"""
        self.assertIn("type: 'logarithmic'", self.script)
        self.assertIn("min: 0.1", self.script)
        # 旧的名次刻度配置必须已移除
        self.assertNotIn("reverse: true", self.script)
        self.assertNotIn("stepSize: 1", self.script)
        self.assertNotIn("max: 10,", self.script)

    def test_dataset_data_uses_token_values_not_ranks(self):
        """折线 data 必须用真实 token 数（零值用 1 替代避免对数轴断开），不能是名次数组。"""
        self.assertIn("tokenValues.map(v => {", self.script)
        self.assertIn("n > 0 ? n : 1", self.script)
        self.assertIn("rankValues: completedRanks[index]", self.script)
        # data 直接等于 completedRanks 的旧写法必须消失
        self.assertNotIn("data: completedRanks[index]", self.script)
        # 旧的 0→null 断开写法必须消失
        self.assertNotIn("Number(v) > 0 ? Number(v) : null", self.script)

    def test_zero_token_days_keep_continuous(self):
        """零用量保留为 0，折线连续不断开（鹏帅要求：没数据默认用 0，不要虚线）。"""
        self.assertIn("spanGaps: true", self.script)
        self.assertNotIn("spanGaps: false", self.script)

    def test_tooltip_keeps_rank_info(self):
        """y 轴丢失名次后，tooltip 必须补偿显示“第 N 名”。"""
        self.assertIn("rankValues[context.dataIndex]", self.script)
        self.assertIn("第 ' + rank + ' 名", self.script)

    def test_tick_labels_use_k_m_yi_format(self):
        """对数刻度标签必须用 K/M/亿 分档，不能用“第 N 名”。"""
        self.assertIn("100000000", self.script)
        self.assertIn("1000000", self.script)
        self.assertIn("1000", self.script)
        self.assertIn("'亿'", self.script)
        self.assertIn("'M'", self.script)
        self.assertIn("'K'", self.script)

    def test_range_leaderboard_has_segment_bar(self):
        """区间总榜必须有柱条 DOM 与 CSS，且用分段刻度（大值线性+小值对数）。"""
        self.assertIn("rank-range-bar", self.html)
        self.assertIn("rank-range-bar-fill", self.html)
        # 分段刻度：SEGMENT_THRESHOLD 阈值 + 大值线性 + 小值对数
        self.assertIn("SEGMENT_THRESHOLD = 1000000", self.script)
        self.assertIn("total >= SEGMENT_THRESHOLD", self.script)
        self.assertIn("50 + (total - SEGMENT_THRESHOLD)", self.script)
        self.assertIn("Math.log10(Math.max(1, total)) / segLog * 50", self.script)
        # 旧的纯对数动态上下界公式必须消失
        self.assertNotIn("loLog", self.script)
        self.assertNotIn("hiLog", self.script)
        self.assertNotIn("logSpan", self.script)

    def test_gap_bridge_plugin_exists(self):
        """必须有 rankHistoryGapBridge 虚线连接插件，用 setLineDash 画断开段虚线。"""
        self.assertIn("rankHistoryGapBridge", self.script)
        self.assertIn("setLineDash([4, 4])", self.script)
        self.assertIn("plugins: [rankHistoryGapBridge, rankHistoryEndLabels]", self.script)

    def test_point_radius_enlarged(self):
        """数据点半径提升以增强层次感（旧值 3/1.5 → 新值 3.5/2）。"""
        self.assertIn("pointRadius: dates.length <= 31 ? 3.5 : 2", self.script)
        self.assertNotIn("pointRadius: dates.length <= 31 ? 3 : 1.5", self.script)

    def test_end_labels_use_rank_values(self):
        """末端标签插件必须从 rankValues 取名次，不能从 data（已是 token）取。"""
        self.assertIn("dataset.rankValues", self.script)
        # 旧的从 data 读名次的写法必须消失
        self.assertNotIn("Number(dataset.data[latestDataIndex]) || 0", self.script)

    def test_end_labels_skip_negative_token_days(self):
        """末端标签跳过负值（tokens < 0），零值用户仍画标签。"""
        self.assertIn("tokens < 0", self.script)
        self.assertNotIn("tokens <= 0", self.script)


class RankLogScaleMathTest(unittest.TestCase):
    """分段刻度柱条宽度计算的数学正确性——大值区线性、小值区对数。

    旧纯对数公式在大值区压缩（2.4亿 vs 70M 差距小）；分段刻度让大值区用线性，
    差距明显（2.4亿=100%、70M≈64%），小值区用对数仍可见。
    """

    def test_big_value_gap_is_obvious(self):
        """2.4亿 vs 70M 都在大值区(≥1M)用线性，差距必须明显（>30%）。"""
        mx = 240_000_000  # 2.4亿为榜首
        w_big = _segment_bar_width(240_000_000, mx)
        w_70m = _segment_bar_width(70_000_000, mx)
        # 2.4亿=100%, 70M=50+(70M-1M)/(2.4亿-1M)*50≈64%
        self.assertAlmostEqual(w_big, 100.0, places=1)
        self.assertGreater(w_70m, 60)
        self.assertLess(w_70m, 70)
        # 差距 >30%，比纯对数（6-7%）明显得多
        self.assertGreater(w_big - w_70m, 30)

    def test_small_value_still_visible(self):
        """小用户(<1M)用对数，仍可见（>0%），不被压成 0。"""
        mx = 240_000_000
        w_100k = _segment_bar_width(100_000, mx)
        w_10 = _segment_bar_width(10, mx)
        # 100K: log10(1e5)/6*50≈41.7%, 10: log10(10)/6*50≈8.3%
        self.assertGreater(w_100k, 40)
        self.assertGreater(w_10, 5)
        self.assertLess(w_10, w_100k)

    def test_magnitude_gap_large_between_yi_and_m(self):
        """1亿 vs 1M 的差距必须远大于 1M vs 0.5M（跨大值/小值区分界）。"""
        mx = 100_000_000
        w_yi = _segment_bar_width(100_000_000, mx)
        w_1m = _segment_bar_width(1_000_000, mx)
        w_half = _segment_bar_width(500_000, mx)
        # 1亿=100%, 1M=50%（分界点）, 0.5M≈47.7%
        self.assertAlmostEqual(w_yi, 100.0, places=1)
        self.assertAlmostEqual(w_1m, 50.0, places=1)
        self.assertGreater(w_yi - w_1m, w_1m - w_half)

    def test_zero_tokens_no_bar(self):
        """零用量不画柱条（宽度 0）。"""
        self.assertEqual(_segment_bar_width(0, 1_000_000), 0.0)

    def test_single_dominant_user_clearly_tallest(self):
        """一个上亿用户 + 一群个位数用户，前者满刻度，后者小但可见。"""
        mx = 300_000_000  # 3亿
        w_dominant = _segment_bar_width(300_000_000, mx)
        w_tiny = _segment_bar_width(10, mx)
        self.assertAlmostEqual(w_dominant, 100.0, places=1)
        # log10(10)/6*50 ≈ 8.3%
        self.assertGreater(w_tiny, 5)
        self.assertGreater(w_dominant - w_tiny, 90)


class CommunityLeaderboardLogBarTest(unittest.TestCase):
    """社区主弹窗「今日活跃用户 Top 10」排行榜的对数柱条契约。"""

    def setUp(self):
        self.html = (ROOT / "index.html").read_text(encoding="utf-8")
        self.windows_html = (ROOT / "go_build" / "static" / "index.html").read_text(
            encoding="utf-8"
        )

    def test_frontends_byte_identical(self):
        self.assertEqual(self.html, self.windows_html)

    def test_leaderboard_has_segment_bar(self):
        """今日排行榜每行必须有柱条，且用分段刻度（大值线性+小值对数）。"""
        # 分段刻度：SEGMENT_THRESHOLD + 大值线性 + 小值对数
        self.assertIn("SEGMENT_THRESHOLD = 1000000", self.html)
        self.assertIn("t>=SEGMENT_THRESHOLD", self.html)
        self.assertIn("50+(t-SEGMENT_THRESHOLD)", self.html)
        self.assertIn("Math.log10(Math.max(1,t))/segLog*50", self.html)
        # 旧的纯对数动态上下界公式必须消失
        self.assertNotIn("loLeaderLog", self.html)
        self.assertNotIn("hiLeaderLog", self.html)
        self.assertNotIn("leaderLogSpan", self.html)
        # 柱条容器与填充
        self.assertIn("今日 Token 分段刻度（量级差距）", self.html)

    def test_leaderboard_grid_has_bar_column(self):
        """排行榜 grid 必须从 4 列扩为 5 列（新增柱条列）。"""
        # 原先 4 列：44px ... 90px；现在 5 列含 52px 柱条列
        self.assertIn("grid-template-columns:44px minmax(100px,1fr) minmax(60px,0.6fr) 52px 80px", self.html)
        # 旧的 4 列布局必须消失
        self.assertNotIn("grid-template-columns:44px minmax(110px,1fr) minmax(70px,0.7fr) 90px", self.html)

    def test_zero_token_member_no_bar(self):
        """零用量成员不画柱条（Number(u.tokens) > 0 判断）。"""
        self.assertIn("Number(u.tokens) > 0", self.html)

    def test_bar_uses_rank_colors(self):
        """柱条颜色用 RANK_LINE_COLORS，与排名趋势弹窗视觉一致。"""
        self.assertIn("RANK_LINE_COLORS[i % RANK_LINE_COLORS.length]", self.html)


if __name__ == "__main__":
    unittest.main()
