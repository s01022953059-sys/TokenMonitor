"""scanner.get_today_usage 聚合字段断言 (v1.4.82 新增)。

验证首页二级菜单新指标所需的后端字段:
  - by_tool[tool]['requests']        每工具调用次数
  - by_model_input[model]            每模型 input token 累计 (算平均上下文)
  - by_model_cached[model]           每模型 cached token 累计 (算缓存命中率)
"""

import unittest
from unittest import mock

import scanner


def _log(tool, model, total, inp, cached, uncached=None, out=None, ts=1000, sess="s1"):
    """构造一条标准日志 dict。"""
    return {
        "tool": tool,
        "model": model,
        "total_tokens": total,
        "input_tokens": inp,
        "input_cached": cached,
        "input_uncached": uncached if uncached is not None else max(0, inp - cached),
        "output_tokens": out if out is not None else (total - inp),
        "timestamp": ts,
        "session_id": sess,
    }


class UsageAggregationTests(unittest.TestCase):
    def _usage_with_logs(self, logs):
        """mock 掉 7 路扫描 + DeepSeek 余额, 让 get_today_usage 只吃给定 logs。"""
        scanners = {
            "scan_cc_switch_logs": mock.Mock(return_value=logs),
            "scan_codex_tokens": mock.Mock(return_value=[]),
            "scan_antigravity_tokens": mock.Mock(return_value=[]),
            "scan_hermes_tokens": mock.Mock(return_value=[]),
            "scan_zcode_tokens": mock.Mock(return_value=[]),
            "scan_minimax_tokens": mock.Mock(return_value=[]),
            "scan_workbuddy_tokens": mock.Mock(return_value=[]),
            "get_deepseek_balance": mock.Mock(return_value={"balance": "0.00", "currency": "CNY", "status": "Offline"}),
        }
        with mock.patch.multiple(scanner, **scanners):
            return scanner.get_today_usage()

    def test_by_tool_carries_request_count(self):
        logs = [
            _log("Codex", "gpt-5", 100, 80, 40, ts=1, sess="a"),
            _log("Codex", "gpt-5", 200, 160, 120, ts=2, sess="b"),
            _log("Claude", "sonnet-4", 50, 40, 0, ts=3, sess="c"),
        ]
        result = self._usage_with_logs(logs)

        self.assertEqual(result["by_tool"]["Codex"]["requests"], 2)
        self.assertEqual(result["by_tool"]["Claude"]["requests"], 1)
        # 原有 token 字段不受影响
        self.assertEqual(result["by_tool"]["Codex"]["total_tokens"], 300)
        self.assertEqual(result["by_tool"]["Codex"]["input_tokens"], 240)

    def test_by_model_input_and_cached_aggregated(self):
        logs = [
            _log("Codex", "gpt-5", 100, 80, 40, ts=1, sess="a"),
            _log("Claude", "gpt-5", 50, 40, 0, ts=2, sess="b"),
            _log("Codex", "glm-5.2", 200, 160, 120, ts=3, sess="c"),
        ]
        result = self._usage_with_logs(logs)

        # gpt-5 来自两条: 80+40=120 input, 40+0=40 cached
        self.assertEqual(result["by_model_input"]["gpt-5"], 120)
        self.assertEqual(result["by_model_cached"]["gpt-5"], 40)
        # glm-5.2 单条: 160 input, 120 cached
        self.assertEqual(result["by_model_input"]["glm-5.2"], 160)
        self.assertEqual(result["by_model_cached"]["glm-5.2"], 120)

        # 平均上下文 = input / requests = 120 / 2 = 60
        self.assertEqual(result["by_model_requests"]["gpt-5"], 2)
        avg_ctx = result["by_model_input"]["gpt-5"] / result["by_model_requests"]["gpt-5"]
        self.assertEqual(avg_ctx, 60)
        # 缓存命中率 = cached / input = 40 / 120 = 33.3%
        hit_rate = result["by_model_cached"]["gpt-5"] / result["by_model_input"]["gpt-5"] * 100
        self.assertAlmostEqual(hit_rate, 33.33, places=1)

    def test_by_model_input_and_cached_keys_match_by_model(self):
        """所有出现在 by_model 的模型, 必须也在 by_model_input/by_model_cached 中。"""
        logs = [
            _log("ZCode", "glm-5.2", 10, 8, 4, ts=1, sess="a"),
            _log("Hermes", "kimi-k2", 20, 16, 8, ts=2, sess="b"),
        ]
        result = self._usage_with_logs(logs)

        self.assertEqual(set(result["by_model_input"].keys()), set(result["by_model"].keys()))
        self.assertEqual(set(result["by_model_cached"].keys()), set(result["by_model"].keys()))

    def test_empty_logs_produce_empty_metric_maps(self):
        result = self._usage_with_logs([])
        self.assertEqual(result["by_tool"], {})
        self.assertEqual(result["by_model_input"], {})
        self.assertEqual(result["by_model_cached"], {})
        self.assertEqual(result["by_model_requests"], {})


if __name__ == "__main__":
    unittest.main()
