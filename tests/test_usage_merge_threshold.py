"""首页工具/模型双圆环的 Other 合并规则契约。

v1.5.18 之前模型维度按 < 1% 合并而工具维度完全不合并, 两边口径不一致:
hy3 当日仅 14 token 也在模型侧以 "Other" 条目出现。现统一为
占比 < 0.1% 合并到 Other, 且被合并工具的模型明细/命中/上下文指标
一并归入 Other 的展开子项。
"""

import json
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

THRESHOLD_LINE = "const mergeThreshold = data.summary.total_tokens * 0.001;"
TOOL_END_MARKER = "const tools = Object.keys(toolMajor)"
MODEL_START_MARKER = "const modelMajor = {};"
MODEL_END_MARKER = "// 将工具→模型矩阵转置"


class UsageMergeThresholdTest(unittest.TestCase):
    def setUp(self):
        self.frontends = {
            "macOS": (ROOT / "index.html").read_text(encoding="utf-8"),
            "Windows": (ROOT / "go_build" / "static" / "index.html").read_text(
                encoding="utf-8"
            ),
        }

    # ---------- 源代码契约 ----------

    def test_frontends_stay_in_sync(self):
        self.assertEqual(self.frontends["macOS"], self.frontends["Windows"])

    def test_unified_threshold_replaces_old_one_percent_rule(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertIn(THRESHOLD_LINE, html)
                self.assertNotIn("onePercent", html)
                self.assertNotIn("工具维度保留所有非零工具", html)

    def test_model_dimension_uses_unified_threshold(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertIn(
                    "data.by_model[m] > 0 && data.by_model[m] >= mergeThreshold", html
                )

    def test_merged_tool_model_matrix_is_remapped_into_other(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                for matrix in (
                    "by_tool_model",
                    "by_tool_model_input",
                    "by_tool_model_cached",
                    "by_tool_model_requests",
                ):
                    self.assertIn(f"remapToolMatrix(data.{matrix})", html)

    def test_help_text_documents_unified_rule(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertIn("工具/模型占比 &lt; 0.1% 自动归入 Other", html)

    # ---------- 行为验证: 提取合并代码块交给 node 执行 ----------

    def _extract_tool_merge_js(self, html):
        head = html.split("const mergeThreshold", 1)[1]
        return "const mergeThreshold" + head.split(TOOL_END_MARKER, 1)[0]

    def _extract_model_merge_js(self, html):
        body = html.split(MODEL_START_MARKER, 1)[1]
        return (
            THRESHOLD_LINE
            + "\n"
            + MODEL_START_MARKER
            + body.split(MODEL_END_MARKER, 1)[0]
        )

    def _run_node(self, js, data, expr):
        script = (
            "const data = "
            + json.dumps(data)
            + ";\n"
            + js
            + "\nconsole.log(JSON.stringify("
            + expr
            + "));"
        )
        proc = subprocess.run(
            ["node", "-e", script], capture_output=True, text=True, timeout=30
        )
        self.assertEqual(0, proc.returncode, proc.stderr)
        return json.loads(proc.stdout)

    def test_tool_dimension_merges_below_threshold_into_other(self):
        js = self._extract_tool_merge_js(self.frontends["macOS"])
        # 阈值 = 3_360_000 * 0.001 = 3360
        data = {
            "summary": {"total_tokens": 3_360_000},
            "by_tool": {
                "ZCode": {"total_tokens": 2_780_000},
                "Claude": {"total_tokens": 514_000},
                "Codex": {"total_tokens": 64_800},
                "TinyTool": {"total_tokens": 100},
                "EdgeTool": {"total_tokens": 3_360},
                "ZeroTool": {"total_tokens": 0},
                "Other": {"total_tokens": 5_000},
            },
        }
        result = self._run_node(js, data, "toolMajor")
        self.assertEqual(
            {
                "ZCode": 2_780_000,
                "Claude": 514_000,
                "Codex": 64_800,
                "EdgeTool": 3_360,
                "Other": 5_100,
            },
            result,
        )

    def test_model_dimension_merges_below_threshold_into_other(self):
        js = self._extract_model_merge_js(self.frontends["macOS"])
        data = {
            "summary": {"total_tokens": 3_360_000},
            "by_model": {
                "glm-5.3-flash": 3_299_062,
                "qwen3.8-flash": 197_342,
                "gpt-5.6-luna": 64_787,
                "hy3": 14,
                "edge-model": 3_360,
                "Other": 5_000,
            },
            "by_model_requests": {"hy3": 2, "glm-5.3-flash": 300},
        }
        result = self._run_node(js, data, "{modelMajor, modelMajorRequests}")
        self.assertEqual(
            {
                "modelMajor": {
                    "glm-5.3-flash": 3_299_062,
                    "qwen3.8-flash": 197_342,
                    "gpt-5.6-luna": 64_787,
                    "edge-model": 3_360,
                    "Other": 5_014,
                },
                "modelMajorRequests": {
                    "glm-5.3-flash": 300,
                    "qwen3.8-flash": 0,
                    "gpt-5.6-luna": 0,
                    "edge-model": 0,
                    "Other": 2,
                },
            },
            result,
        )

    def test_zero_usage_models_never_render(self):
        js = self._extract_model_merge_js(self.frontends["macOS"])
        data = {
            "summary": {"total_tokens": 0},
            "by_model": {"a": 0},
            "by_model_requests": {},
        }
        result = self._run_node(js, data, "{modelMajor, modelMajorRequests}")
        self.assertEqual({"modelMajor": {}, "modelMajorRequests": {}}, result)


if __name__ == "__main__":
    unittest.main()
