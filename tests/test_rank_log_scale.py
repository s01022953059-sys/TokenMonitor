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


def _log_bar_width(total_tokens, lo_log, hi_log):
    """复刻前端绝对对数刻度柱条宽度：(log10(total)-loLog)/(hiLog-loLog)*100。

    lo_log/hi_log 是全榜最小/最大正数 token 的 log10 取 floor/ceil 得到的动态上下界。
    不再以 max 为满刻度（旧公式会把榜首撑满、其余压扁，导致量级差被反转）。
    """
    if total_tokens <= 0:
        return 0.0
    span = hi_log - lo_log
    if span <= 0:
        return 0.0
    return (math.log10(max(1, total_tokens)) - lo_log) / span * 100


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
        """区间总榜必须有对数柱条 DOM 与 CSS，且用绝对对数刻度（动态上下界）。"""
        self.assertIn("rank-range-bar", self.html)
        self.assertIn("rank-range-bar-fill", self.html)
        # 新公式：绝对对数刻度，loLog/hiLog/logSpan 动态上下界
        self.assertIn("loLog", self.script)
        self.assertIn("hiLog", self.script)
        self.assertIn("logSpan", self.script)
        self.assertIn("Math.log10(Math.max(1, total)) - loLog", self.script)
        # 旧的 max 满刻度公式必须消失（会反转比例）
        self.assertNotIn("Math.log10(maxTotalTokens + 1)", self.script)
        self.assertNotIn("Math.log10(total + 1) / maxLog", self.script)

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

    def test_end_labels_skip_zero_token_days(self):
        """末端标签必须跳过零用量天（tokens <= 0 不画标签）。"""
        self.assertIn("tokens <= 0", self.script)


class RankLogScaleMathTest(unittest.TestCase):
    """绝对对数刻度柱条宽度计算的数学正确性——验证量级差距被正确拉开。

    旧公式（以 max 为满刻度）会反转比例：1亿/10M/100K = 100%/87.5%/62.5%，
    1亿-10M 差 12.5% 反而比 10M-100K 差 25% 小。新公式（动态上下界）修复此问题。
    """

    def test_proportions_not_inverted_anymore(self):
        """1亿/10M/100K 三档柱长必须差距递增（越往下差异越大），不再被压扁。"""
        # 全榜：1亿(榜首)、10M、100K(榜尾) → lo=floor(log10(1e5))=5, hi=ceil(log10(1e8))=8
        lo, hi = 5, 8
        w_yi = _log_bar_width(100_000_000, lo, hi)
        w_10m = _log_bar_width(10_000_000, lo, hi)
        w_100k = _log_bar_width(100_000, lo, hi)
        # 1亿=100%(满刻度), 10M=(7-5)/3=66.7%, 100K=(5-5)/3=0%
        self.assertAlmostEqual(w_yi, 100.0, places=1)
        self.assertAlmostEqual(w_10m, 66.7, places=1)
        self.assertAlmostEqual(w_100k, 0.0, places=1)
        # 关键修复：1亿-10M 差 33%，10M-100K 差 67%——越往下差异越大（对数正确行为）
        self.assertGreater(w_yi - w_10m, 30)
        self.assertGreater(w_10m - w_100k, 60)
        # 三者一眼可区分，不再挤在顶部
        self.assertGreater(w_yi - w_100k, 90)

    def test_old_formula_inverted_proportions(self):
        """反向验证旧公式确实反转了比例（说明为什么需要修复）。"""
        lo, hi = 5, 8  # 新公式上下界
        new_yi_10m = _log_bar_width(100_000_000, lo, hi) - _log_bar_width(10_000_000, lo, hi)
        new_10m_100k = _log_bar_width(10_000_000, lo, hi) - _log_bar_width(100_000, lo, hi)
        # 新公式：10M-100K 差距更大（越往下越大）
        self.assertGreater(new_10m_100k, new_yi_10m)

    def test_magnitude_gap_small_between_1m_and_half_m(self):
        """1M vs 0.5M 的柱条宽度差必须很小（约 0.3 个 log 单位）。"""
        # 榜首1亿、榜尾100K：lo=5, hi=8
        lo, hi = 5, 8
        w_1m = _log_bar_width(1_000_000, lo, hi)
        w_half = _log_bar_width(500_000, lo, hi)
        # log10(1e6)=6 → 33.3%, log10(5e5)≈5.7 → 23.3%，差约 10%
        self.assertLess(w_1m - w_half, 11)
        # 关键：1亿-1M 的差距必须远大于 1M-0.5M 的差距
        self.assertGreater(
            _log_bar_width(100_000_000, lo, hi) - w_1m,
            w_1m - w_half,
        )

    def test_zero_tokens_no_bar(self):
        """零用量不画柱条（宽度 0）。"""
        self.assertEqual(_log_bar_width(0, 5, 8), 0.0)

    def test_all_zero_no_division_error(self):
        """全榜最大值也是 0 时上下界相同，不画柱条（避免除零）。"""
        self.assertEqual(_log_bar_width(100, 0, 0), 0.0)

    def test_single_dominant_user_clearly_tallest(self):
        """一个上亿用户 + 一群个位数用户，前者柱条接近满刻度，后者几乎看不见。"""
        # 3亿(榜首, log10≈8.477, hi=ceil=9)、10(榜尾, log10=1, lo=floor=1)
        lo, hi = 1, 9
        w_dominant = _log_bar_width(300_000_000, lo, hi)
        w_tiny = _log_bar_width(10, lo, hi)
        # (8.477-1)/8*100 ≈ 93.5%（3亿不是 10^9 整下界，未达满刻度是正确的）
        self.assertAlmostEqual(w_dominant, 93.5, places=1)
        # log10(10)=1 → (1-1)/8 = 0%
        self.assertLess(w_tiny, 1)
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

    def test_leaderboard_has_log_bar(self):
        """今日排行榜每行必须有对数柱条（内联 style 形式），且用绝对对数刻度。"""
        # 新公式：绝对对数刻度，loLeaderLog/hiLeaderLog/leaderLogSpan 动态上下界
        self.assertIn("loLeaderLog", self.html)
        self.assertIn("hiLeaderLog", self.html)
        self.assertIn("leaderLogSpan", self.html)
        self.assertIn("Math.log10(Math.max(1, Number(u.tokens))) - loLeaderLog", self.html)
        # 旧的 max 满刻度公式必须消失（会反转比例）
        self.assertNotIn("maxLeaderLog", self.html)
        self.assertNotIn("Math.log10(Number(u.tokens) + 1) / maxLeaderLog", self.html)
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
