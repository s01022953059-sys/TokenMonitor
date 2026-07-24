"""ZCode 与 MiniMax Code 数据源回归测试。

覆盖维度:
1. ZCode: 毫秒时间戳转秒、Anthropic 式缓存口径、status 过滤
2. MiniMax Code: ISO 时间戳转秒、message.usage 字段、只取 assistant message
3. 跨源去重: 新工具与 cc-switch 不产生误合并
4. 社区上报: by_tool 动态包含新工具, today_tokens 含新工具用量
"""
import datetime
import json
import os
import sqlite3
import tempfile
import unittest
from unittest import mock

import scanner
import community


def _make_zcode_db(path, rows):
    """在 path 创建一个只含 model_usage 表的 ZCode SQLite。"""
    conn = sqlite3.connect(path)
    conn.execute("""
        CREATE TABLE model_usage (
            started_at integer, model_id text, status text,
            input_tokens integer, output_tokens integer,
            reasoning_tokens integer,
            cache_creation_input_tokens integer,
            cache_read_input_tokens integer,
            session_id text, turn_id text
        )
    """)
    conn.executemany(
        "INSERT INTO model_usage VALUES (?,?,?,?,?,?,?,?,?,?)", rows
    )
    conn.commit()
    conn.close()


class ZCodeScannerTests(unittest.TestCase):
    """ZCode 数据源: 毫秒时间戳、Anthropic 缓存口径、status 过滤。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, "db.sqlite")
        self.patcher = mock.patch.object(scanner, "ZCODE_DB_PATH", self.db_path)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def test_millisecond_timestamp_converted_to_seconds(self):
        """started_at 是毫秒, 返回的 timestamp 必须是秒 (1.7e9 量级)。"""
        _make_zcode_db(self.db_path, [
            # 1800000000000 ms = 1800000000 s
            (1800000000000, "glm-5.2", "completed", 100, 20, 0, 0, 50, "s1", "t1"),
        ])
        events = scanner.scan_zcode_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["timestamp"], 1800000000)
        self.assertLess(events[0]["timestamp"], 2_000_000_000)

    def test_anthropic_cache_split(self):
        """input 不含 cache, cache_read/cache_creation 是独立字段。"""
        _make_zcode_db(self.db_path, [
            (1800000000000, "glm-5.2", "completed",
             100, 20, 10, 30, 50, "s1", "t1"),
        ])
        events = scanner.scan_zcode_tokens(1_700_000_000)
        e = events[0]
        # input_uncached = input(100) + reasoning(10) = 110
        self.assertEqual(e["input_uncached"], 110)
        # input_cached = cache_read(50) + cache_creation(30) = 80
        self.assertEqual(e["input_cached"], 80)
        # total_input = 110 + 80 = 190
        self.assertEqual(e["input_tokens"], 190)
        # total = input(190) + output(20) = 210
        self.assertEqual(e["total_tokens"], 210)

    def test_skips_running_and_cancelled(self):
        """running 和 cancelled 不计入; completed 和 error 计入。"""
        _make_zcode_db(self.db_path, [
            (1800000000000, "glm-5.2", "running", 100, 0, 0, 0, 0, "s1", "t1"),
            (1800000001000, "glm-5.2", "cancelled", 200, 0, 0, 0, 0, "s2", "t2"),
            (1800000002000, "glm-5.2", "completed", 300, 10, 0, 0, 0, "s3", "t3"),
            (1800000003000, "glm-5.2", "error", 400, 5, 0, 0, 0, "s4", "t4"),
        ])
        events = scanner.scan_zcode_tokens(1_700_000_000)
        self.assertEqual(len(events), 2)  # completed + error
        totals = sorted(e["input_tokens"] for e in events)
        self.assertEqual(totals, [300, 400])

    def test_model_name_normalized(self):
        """GLM-5.2 / glm-5.2 等大小写变体经 normalize_model_name 合并。"""
        _make_zcode_db(self.db_path, [
            (1800000000000, "GLM-5.2", "completed", 100, 10, 0, 0, 0, "s1", "t1"),
            (1800000001000, "glm-5.2", "completed", 200, 10, 0, 0, 0, "s2", "t2"),
        ])
        events = scanner.scan_zcode_tokens(1_700_000_000)
        models = {e["model"] for e in events}
        self.assertEqual(models, {"glm-5.2"})

    def test_end_timestamp_filters_range(self):
        """end_timestamp 参数正确过滤上界。"""
        _make_zcode_db(self.db_path, [
            (1800000000000, "glm-5.2", "completed", 100, 10, 0, 0, 0, "s1", "t1"),
            (1800000005000, "glm-5.2", "completed", 200, 10, 0, 0, 0, "s2", "t2"),
        ])
        events = scanner.scan_zcode_tokens(1_700_000_000, 1800000003)
        self.assertEqual(len(events), 1)


class MiniMaxScannerTests(unittest.TestCase):
    """MiniMax Code (Pi Agent) 数据源: ISO 时间戳、message.usage、assistant 过滤。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.sessions_dir = os.path.join(self.temp_dir.name, "sessions", "proj")
        os.makedirs(self.sessions_dir)
        self.patcher = mock.patch.object(scanner, "MINIMAX_SESSIONS_DIR",
                                         os.path.join(self.temp_dir.name, "sessions"))
        self.patcher.start()
        self.addCleanup(self.patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def _write_session(self, name, events):
        path = os.path.join(self.sessions_dir, name)
        with open(path, "w", encoding="utf-8") as stream:
            for event in events:
                stream.write(json.dumps(event) + "\n")
        return path

    def test_iso_timestamp_converted_to_seconds(self):
        """ISO 8601 字符串转成 unix 秒, 与其他源对齐。"""
        self._write_session("s1.jsonl", [
            {"type": "session", "timestamp": "2026-05-26T10:00:00.000Z"},
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        # 2026-05-26T10:00:01Z → 本地时区 unix 秒 (astimezone 转本地)
        expected = int(datetime.datetime.fromisoformat(
            "2026-05-26T10:00:01.000+00:00").astimezone().timestamp())
        self.assertEqual(events[0]["timestamp"], expected)

    def test_only_assistant_messages_counted(self):
        """user message 没有 usage, 不应产生事件。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:00.000Z",
             "message": {"role": "user", "content": "hello"}},
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
            {"type": "model_change", "timestamp": "2026-05-26T10:00:00.500Z"},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)

    def test_anthropic_cache_split(self):
        """input 不含 cache, cacheRead/cacheWrite 独立。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20,
                                   "cacheRead": 50, "cacheWrite": 30,
                                   "totalTokens": 200}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        e = events[0]
        self.assertEqual(e["input_uncached"], 100)
        self.assertEqual(e["input_cached"], 80)  # 50 + 30
        self.assertEqual(e["input_tokens"], 180)  # 100 + 80
        self.assertEqual(e["total_tokens"], 200)  # 180 + 20

    def test_total_tokens_fallback_when_zero(self):
        """totalTokens=0 时回退为 input+output+cache。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20,
                                   "cacheRead": 0, "cacheWrite": 0,
                                   "totalTokens": 0}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["total_tokens"], 120)

    def test_skips_message_without_usage(self):
        """assistant message 但没有 usage 字段, 不应产生事件。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5"}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 0)

    def test_end_timestamp_filters_range(self):
        """end_timestamp 参数正确过滤上界。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
            {"type": "message", "timestamp": "2026-05-26T11:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 200, "output": 20, "totalTokens": 220}}},
        ])
        # end = 10:30:00 本地时区, 只保留 10:00:01
        end_ts = int(datetime.datetime.fromisoformat(
            "2026-05-26T10:30:00.000+00:00").astimezone().timestamp())
        events = scanner.scan_minimax_tokens(1_700_000_000, end_ts)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["total_tokens"], 120)


class CrossSourceDedupTests(unittest.TestCase):
    """新工具与 cc-switch / 其他源不产生误合并或漏算。"""

    def test_new_sources_not_silently_merged_by_dedup(self):
        """ZCode 和 MiniMax Code 的独立请求不应被 cc-switch 去重掉。

        构造一个 ZCode 事件和一个 cc-switch 事件, 时间相近但 total 不同,
        确认 _dedup_events 保留两者 (不同请求)。
        """
        base_ts = 1800000000
        events = [
            {"timestamp": base_ts, "tool": "ZCode", "model": "glm-5.2",
             "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
             "input_cached": 0, "input_uncached": 100, "session_id": "zc1"},
            {"timestamp": base_ts, "tool": "Codex", "model": "gpt-5.6",
             "input_tokens": 200, "output_tokens": 50, "total_tokens": 250,
             "input_cached": 0, "input_uncached": 200, "session_id": "cc1"},
        ]
        deduped = scanner._dedup_events(events)
        self.assertEqual(len(deduped), 2)  # total 不同, 不去重

    def test_same_request_across_sources_deduped(self):
        """同一笔请求 (相同时间±2s + 相同 total) 只计一次。"""
        base_ts = 1800000000
        events = [
            {"timestamp": base_ts, "tool": "ZCode", "model": "glm-5.2",
             "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
             "input_cached": 0, "input_uncached": 100, "session_id": "zc1"},
            {"timestamp": base_ts + 1, "tool": "Codex", "model": "glm-5.2",
             "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
             "input_cached": 0, "input_uncached": 100, "session_id": "cc1"},
        ]
        deduped = scanner._dedup_events(events)
        self.assertEqual(len(deduped), 1)  # 相同请求, 去重

    def test_new_sources_included_in_today_usage(self):
        """get_today_usage 的 by_tool 动态包含有用量的新工具。"""
        zcode_event = {
            "timestamp": int(datetime.datetime.now().timestamp()),
            "tool": "ZCode", "model": "glm-5.2",
            "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
            "input_cached": 0, "input_uncached": 100, "session_id": "zc1",
        }
        minimax_event = {
            "timestamp": int(datetime.datetime.now().timestamp()),
            "tool": "MiniMax Code", "model": "gpt-5.5",
            "input_tokens": 200, "output_tokens": 30, "total_tokens": 230,
            "input_cached": 0, "input_uncached": 200, "session_id": "mm1",
        }
        empty = mock.Mock(return_value=[])
        with mock.patch.object(scanner, "scan_cc_switch_logs", empty), \
             mock.patch.object(scanner, "scan_codex_tokens", empty), \
             mock.patch.object(scanner, "scan_antigravity_tokens", empty), \
             mock.patch.object(scanner, "scan_hermes_tokens", empty), \
             mock.patch.object(scanner, "scan_zcode_tokens", return_value=[zcode_event]), \
             mock.patch.object(scanner, "scan_minimax_tokens", return_value=[minimax_event]), \
             mock.patch.object(scanner, "scan_workbuddy_tokens", empty), \
             mock.patch.object(scanner, "get_deepseek_balance", return_value={}):
            usage = scanner.get_today_usage()

        self.assertIn("ZCode", usage["by_tool"])
        self.assertIn("MiniMax Code", usage["by_tool"])
        self.assertEqual(usage["by_tool"]["ZCode"]["total_tokens"], 120)
        self.assertEqual(usage["by_tool"]["MiniMax Code"]["total_tokens"], 230)
        # summary.total_tokens 含新工具
        self.assertEqual(usage["summary"]["total_tokens"], 350)


class AppTypeNormalizationTests(unittest.TestCase):
    """cc-switch app_type 归一化: 新工具不能被误归为 Other。

    回归场景: ZCode 请求走 cc-switch 代理时 app_type=zcode,
    _normalize_app_type('zcode') 之前返回 'Other', 导致社区排行
    显示 'Other + Codex' 而非 'ZCode + Codex'。
    """

    def test_zcode_app_type_not_other(self):
        """zcode / ZCode 必须归为 'ZCode', 不能是 'Other'。"""
        self.assertEqual(scanner._normalize_app_type("zcode"), "ZCode")
        self.assertEqual(scanner._normalize_app_type("ZCode"), "ZCode")
        self.assertEqual(scanner._normalize_app_type("ZCODE"), "ZCode")

    def test_minimax_app_type_not_other(self):
        """minimax / MiniMax Code 走代理时不能归为 'Other'。"""
        self.assertEqual(scanner._normalize_app_type("minimax"), "MiniMax Code")
        self.assertEqual(scanner._normalize_app_type("minimax-code"), "MiniMax Code")

    def test_existing_mappings_unchanged(self):
        """已有映射不被破坏。"""
        self.assertEqual(scanner._normalize_app_type("codex"), "Codex")
        self.assertEqual(scanner._normalize_app_type("claude"), "Claude")
        self.assertEqual(scanner._normalize_app_type("claude-desktop"), "Claude")
        self.assertEqual(scanner._normalize_app_type("hermes"), "Hermes")
        self.assertEqual(scanner._normalize_app_type("workbuddy"), "WorkBuddy")
        self.assertEqual(scanner._normalize_app_type("opencode"), "OpenCode")

    def test_empty_and_unknown_still_other(self):
        """空值和未知类型仍归为 Other。"""
        self.assertEqual(scanner._normalize_app_type(""), "Other")
        self.assertEqual(scanner._normalize_app_type(None), "Other")
        self.assertEqual(scanner._normalize_app_type("unknown-tool"), "Other")


class CommunityReportTests(unittest.TestCase):
    """社区上报: by_tool 动态包含新工具, today_tokens 含新工具用量。"""

    def test_format_report_tools_includes_new_sources(self):
        """_format_report_tools 动态展示所有 total_tokens > 0 的工具。"""
        by_tool = {
            "ZCode": 150000000,
            "Codex": 9000000,
            "MiniMax Code": 60000000,
        }
        result = community._format_report_tools(by_tool)
        # 按用量降序: ZCode + MiniMax Code + Codex
        self.assertEqual(result, "ZCode + MiniMax Code + Codex")

    def test_format_report_tools_excludes_zero(self):
        """total_tokens=0 的工具不出现在工具列。"""
        by_tool = {"ZCode": 100, "Codex": 0, "MiniMax Code": 50}
        result = community._format_report_tools(by_tool)
        self.assertNotIn("Codex", result)
        self.assertIn("ZCode", result)
        self.assertIn("MiniMax Code", result)

    def test_report_today_tokens_includes_new_sources(self):
        """社区上报的 today_tokens 必须包含新工具的用量。"""
        zcode_event = {
            "timestamp": int(datetime.datetime.now().timestamp()),
            "tool": "ZCode", "model": "glm-5.2",
            "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
            "input_cached": 0, "input_uncached": 100, "session_id": "zc1",
        }
        empty = mock.Mock(return_value=[])
        with mock.patch.object(scanner, "scan_cc_switch_logs", empty), \
             mock.patch.object(scanner, "scan_codex_tokens", empty), \
             mock.patch.object(scanner, "scan_antigravity_tokens", empty), \
             mock.patch.object(scanner, "scan_hermes_tokens", empty), \
             mock.patch.object(scanner, "scan_zcode_tokens", return_value=[zcode_event]), \
             mock.patch.object(scanner, "scan_minimax_tokens", empty), \
             mock.patch.object(scanner, "scan_workbuddy_tokens", empty), \
             mock.patch.object(scanner, "get_deepseek_balance", return_value={}):
            usage = scanner.get_today_usage()

        # 模拟 report_community_stats 提取 today_tokens
        today_tokens = usage["summary"]["total_tokens"]
        by_tool = {k: v.get("total_tokens", 0) for k, v in usage["by_tool"].items()}
        self.assertEqual(today_tokens, 120)
        self.assertIn("ZCode", by_tool)
        # 工具列应显示 ZCode
        self.assertEqual(community._format_report_tools(by_tool), "ZCode")


if __name__ == "__main__":
    unittest.main()
