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


def _log_bar_width(total_tokens, max_total_tokens):
    """复刻前端对数柱条宽度计算：log10(total+1)/log10(max+1)*100。"""
    if total_tokens <= 0 or max_total_tokens <= 0:
        return 0.0
    return math.log10(total_tokens + 1) / math.log10(max_total_tokens + 1) * 100


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
        """主图 y 轴必须是对数刻度，不能残留 linear 名次配置。"""
        self.assertIn("type: 'logarithmic'", self.script)
        self.assertIn("min: 100", self.script)
        # 旧的名次刻度配置必须已移除
        self.assertNotIn("reverse: true", self.script)
        self.assertNotIn("stepSize: 1", self.script)
        self.assertNotIn("min: 1,", self.script)
        self.assertNotIn("max: 10,", self.script)

    def test_dataset_data_uses_token_values_not_ranks(self):
        """折线 data 必须用真实 token 数（零值转 null 断开），不能是名次数组。"""
        self.assertIn("tokenValues.map(v => (Number(v) > 0 ? Number(v) : null))", self.script)
        self.assertIn("rankValues: completedRanks[index]", self.script)
        # data 直接等于 completedRanks 的旧写法必须消失
        self.assertNotIn("data: completedRanks[index]", self.script)

    def test_zero_token_days_disconnect_line(self):
        """零用量天必须断开折线（spanGaps: false），符合 D-2026-07-31-01 不展示无意义数据。"""
        self.assertIn("spanGaps: false", self.script)
        self.assertNotIn("spanGaps: true", self.script)

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

    def test_range_leaderboard_has_log_bar(self):
        """区间总榜必须有对数柱条 DOM 与 CSS。"""
        self.assertIn("rank-range-bar", self.html)
        self.assertIn("rank-range-bar-fill", self.html)
        self.assertIn("Math.log10(total + 1)", self.script)
        self.assertIn("Math.log10(maxTotalTokens + 1)", self.script)

    def test_end_labels_use_rank_values(self):
        """末端标签插件必须从 rankValues 取名次，不能从 data（已是 token）取。"""
        self.assertIn("dataset.rankValues", self.script)
        # 旧的从 data 读名次的写法必须消失
        self.assertNotIn("Number(dataset.data[latestDataIndex]) || 0", self.script)

    def test_end_labels_skip_zero_token_days(self):
        """末端标签必须跳过零用量天（tokens <= 0 不画标签）。"""
        self.assertIn("tokens <= 0", self.script)


class RankLogScaleMathTest(unittest.TestCase):
    """对数柱条宽度计算的数学正确性——验证量级差距被正确放大。"""

    def test_magnitude_gap_large_between_yi_and_m(self):
        """1亿 vs 1M 的柱条宽度差必须明显（约 2 个 log 单位 = 满刻度的 ~25%）。"""
        max_total = 100_000_000  # 1亿为全榜最大
        w_yi = _log_bar_width(100_000_000, max_total)   # 1亿
        w_1m = _log_bar_width(1_000_000, max_total)     # 1M
        # log10(1e8+1)/log10(1e8+1) ≈ 1.0 (100%)
        # log10(1e6+1)/log10(1e8+1) ≈ 6/8 = 0.75 (75%)
        self.assertAlmostEqual(w_yi, 100.0, places=1)
        self.assertAlmostEqual(w_1m, 75.0, places=1)
        # 差距 25 个百分点，视觉上明显
        self.assertGreater(w_yi - w_1m, 20)

    def test_magnitude_gap_small_between_1m_and_half_m(self):
        """1M vs 0.5M 的柱条宽度差必须很小（约 0.3 个 log 单位 = 满刻度的 ~4%）。"""
        max_total = 100_000_000
        w_1m = _log_bar_width(1_000_000, max_total)
        w_half = _log_bar_width(500_000, max_total)
        # log10(1e6)≈6, log10(5e5)≈5.7, 差 0.3 / 8 ≈ 3.75%
        self.assertLess(w_1m - w_half, 6)
        # 关键：1亿-1M 的差距必须远大于 1M-0.5M 的差距
        self.assertGreater(
            (w_yi := _log_bar_width(100_000_000, max_total)) - w_1m,
            w_1m - w_half,
        )

    def test_linear_would_distort_this(self):
        """反向验证：线性刻度下 1亿-1M 差距和 1M-0.5M 差距的比例被扭曲。"""
        # 线性下：1亿-1M=9900万，1M-0.5M=50万，比值 198:1
        linear_big = 100_000_000 - 1_000_000
        linear_small = 1_000_000 - 500_000
        self.assertGreater(linear_big / linear_small, 100)
        # 但归一化后线性差距：1亿占 100%，1M 占 1%，0.5M 占 0.5%
        # 1亿-1M 线性差 99%，1M-0.5M 线性差 0.5% —— 0.5M 的柱子几乎不可见
        # 对数下三者都能看见，这正是对数的价值

    def test_zero_tokens_no_bar(self):
        """零用量不画柱条（宽度 0）。"""
        self.assertEqual(_log_bar_width(0, 1_000_000), 0.0)

    def test_zero_max_no_bar(self):
        """全榜最大值也是 0 时不画柱条（避免除零）。"""
        self.assertEqual(_log_bar_width(100, 0), 0.0)

    def test_single_dominant_user_clearly_tallest(self):
        """一个上亿用户 + 一群个位数用户，前者柱条必须接近满刻度，后者几乎看不见。"""
        max_total = 300_000_000  # 3亿
        w_dominant = _log_bar_width(300_000_000, max_total)
        w_tiny = _log_bar_width(10, max_total)
        self.assertAlmostEqual(w_dominant, 100.0, places=1)
        # log10(11)/log10(3e8+1) ≈ 1.04/8.48 ≈ 12%
        self.assertLess(w_tiny, 13)
        self.assertGreater(w_dominant - w_tiny, 85)


class CommunityLeaderboardLogBarTest(unittest.TestCase):
    """社区主弹窗「今日活跃用户 Top 10」排行榜的对数柱条契约。"""

    def setUp(self):
        self.html = (ROOT / "index.html").read_text(encoding="utf-8")
        self.windows_html = (ROOT / "go_build" / "static" / "index.html").read_text(
            encoding="utf-8"
        )

    def test_frontends_byte_identical(self):
        self.assertEqual(self.html, self.windows_html)

    def test_leaderboard_has_log_bar(self):
        """今日排行榜每行必须有对数柱条（内联 style 形式）。"""
        # maxLeaderLog 归一化基准必须存在
        self.assertIn("maxLeaderLog", self.html)
        self.assertIn("Math.log10(maxLeaderTokens + 1)", self.html)
        # 每行柱条宽度计算：log10(tokens+1)/maxLeaderLog
        self.assertIn("Math.log10(Number(u.tokens) + 1) / maxLeaderLog", self.html)
        # 柱条容器与填充
        self.assertIn("今日 Token 对数刻度（量级差距）", self.html)

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
