"""Antigravity 数据源的双端显示与接线契约。

v1.5.20 重新接入 Antigravity (antigravity-tools 代理库) 时的口径分裂防线:
1. 五处聚合点 (今日/历史/会话列表/热力图/热力图详情) 双端都必须接线,
   否则出现"首页有 Antigravity、热力图没有"的分裂
   (对齐 test_history_and_heatmap_use_the_same_event_set 的语义)。
2. 前端两份 index.html 必须同步包含: About 平台表行、社区页工具色盘、
   主 toolColors 紫色键; legacy 'gemini 3.5 flash' 残留必须清掉。
"""

import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class AntigravityWiringContractTest(unittest.TestCase):
    def setUp(self):
        self.scanner = (ROOT / "scanner.py").read_text(encoding="utf-8")
        self.main_go = (ROOT / "go_build" / "main.go").read_text(encoding="utf-8")
        self.frontends = {
            "macOS": (ROOT / "index.html").read_text(encoding="utf-8"),
            "Windows": (ROOT / "go_build" / "static" / "index.html").read_text(
                encoding="utf-8"
            ),
        }

    # ---------- 后端接线 ----------

    def test_python_five_aggregation_points_wired(self):
        # get_today_usage / get_historical_usage / get_session_list /
        # get_heatmap_data / get_heatmap_detail 各一处调用
        self.assertGreaterEqual(
            self.scanner.count("scan_antigravity_tokens("), 6,
            "scanner.py 聚合点接线不足 (定义 1 处 + 调用至少 5 处)",
        )

    def test_go_five_aggregation_points_wired(self):
        # getTodayUsage / getHistoricalUsage / getSessionList /
        # getHeatmapData / getHeatmapDetail 各一处调用
        self.assertGreaterEqual(
            self.main_go.count("scanAntigravityTokens("), 6,
            "main.go 聚合点接线不足 (定义 1 处 + 调用至少 5 处)",
        )

    def test_tool_default_lists_include_antigravity(self):
        # 历史趋势默认工具列表双端对齐 (插在 Other 之前)
        self.assertIn('"WorkBuddy", "Antigravity", "Other"]', self.scanner)
        self.assertIn('"WorkBuddy", "Antigravity", "Other"}', self.main_go)

    def test_db_path_constants_aligned(self):
        self.assertIn('~/.antigravity_tools/token_stats.db', self.scanner)
        self.assertIn('".antigravity_tools", "token_stats.db"', self.main_go)

    # ---------- 前端显示 ----------

    def test_frontends_stay_in_sync(self):
        self.assertEqual(self.frontends["macOS"], self.frontends["Windows"])

    def test_about_platform_table_has_antigravity_row(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertIn("#a855f7;margin-right:4px;\"></span>Antigravity", html)
                self.assertIn("~/.antigravity_tools/token_stats.db</code>", html)

    def test_tool_color_maps_have_antigravity(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                # 主色盘 (lowercase 匹配) + 社区页局部色盘 (精确显示名匹配)
                self.assertIn("'antigravity': '#a855f7'", html)
                self.assertIn("'Antigravity': '#a855f7'", html)

    def test_legacy_gemini_flash_color_removed(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertNotIn("'gemini 3.5 flash'", html)


class LegendAlignmentContractTest(unittest.TestCase):
    """v1.5.22: 图例数字+百分比中轴对齐契约。

    旧版整串右对齐导致数字列跨行参差; 现数字右对齐到中轴、百分比左对齐
    离开中轴 (固定 ch 列宽保证跨行垂直对齐), 主图例行与二级子行共用
    .metric-pair/.metric-num/.metric-pct。浏览器实测: 可见行数字右边缘、
    百分比左边缘、整组中心各为单一值。
    """

    def setUp(self):
        self.frontends = {
            "macOS": (ROOT / "index.html").read_text(encoding="utf-8"),
            "Windows": (ROOT / "go_build" / "static" / "index.html").read_text(
                encoding="utf-8"
            ),
        }

    def test_metric_pair_classes_present(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                for needle in ('tool-value metric-pair', 'legend-detail-value metric-pair',
                               'class="metric-num"', 'class="metric-pct"',
                               '.metric-pair {', '.metric-num {', '.metric-pct {'):
                    self.assertIn(needle, html)

    def test_old_inline_percent_span_removed(self):
        # 旧的整串右对齐写法 (margin-left: 4px 的内联百分比 span) 必须消失
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertNotIn('margin-left: 4px;">(${percent}%)', html)


if __name__ == "__main__":
    unittest.main()
