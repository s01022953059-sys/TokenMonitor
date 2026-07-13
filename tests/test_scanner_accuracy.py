import datetime
import json
import os
import sqlite3
import tempfile
import unittest
from unittest import mock

import scanner


class ScannerAccuracyTests(unittest.TestCase):
    def test_total_pages_keeps_a_page_for_empty_results(self):
        self.assertEqual(scanner._total_pages(0, 50), 1)
        self.assertEqual(scanner._total_pages(51, 50), 2)
        self.assertEqual(scanner._total_pages(5, 0), 1)

    def test_codex_session_keeps_event_model_after_provider_switch(self):
        provider_models = {"current-provider": "gpt-5.5"}
        self.assertEqual(
            scanner._resolve_cc_model("_codex_session", "gpt-5.6-sol", provider_models),
            "gpt-5.6-sol",
        )
        self.assertEqual(
            scanner._resolve_cc_model("current-provider", "gpt-declared", provider_models),
            "gpt-5.5",
        )

    def test_cc_switch_handles_anthropic_and_openai_cache_semantics(self):
        anthropic = scanner._cc_token_breakdown("claude", 100, 30, 500, 20)
        openai = scanner._cc_token_breakdown("codex", 1_000, 50, 600, 0)

        self.assertEqual(anthropic, (620, 30, 650, 500, 120))
        self.assertEqual(openai, (1_000, 50, 1_050, 600, 400))

    def test_workbuddy_reads_per_request_project_usage(self):
        with tempfile.TemporaryDirectory() as root:
            project = os.path.join(root, "project")
            os.makedirs(project)
            path = os.path.join(project, "session-test.jsonl")
            timestamp_ms = 1_800_000_000_000
            item = {
                "type": "message",
                "timestamp": timestamp_ms,
                "role": "assistant",
                "providerData": {
                    "model": "MiniMax-M3",
                    "usage": {
                        "inputTokens": 100,
                        "outputTokens": 20,
                        "totalTokens": 120,
                        "inputTokensDetails": [{"cached_tokens": 60}],
                    },
                },
            }
            with open(path, "w", encoding="utf-8") as stream:
                stream.write(json.dumps(item) + "\n")

            with mock.patch.object(scanner, "WORKBUDDY_PROJECTS_DIR", root):
                events = scanner.scan_workbuddy_tokens(1_700_000_000)

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["model"], "minimax-m3")
        self.assertEqual(events[0]["total_tokens"], 120)
        self.assertEqual(events[0]["input_cached"], 60)
        self.assertEqual(events[0]["session_id"], "session-test")

    def test_history_and_heatmap_use_the_same_event_set(self):
        timestamp = int(datetime.datetime.now().timestamp())
        event = {
            "time": "12:00:00", "timestamp": timestamp, "tool": "WorkBuddy",
            "model": "minimax-m3", "input_tokens": 100, "output_tokens": 20,
            "total_tokens": 120, "input_cached": 0, "input_uncached": 100,
        }
        empty = mock.Mock(return_value=[])
        patches = [
            mock.patch.object(scanner, "scan_cc_switch_logs", empty),
            mock.patch.object(scanner, "scan_codex_tokens", empty),
            mock.patch.object(scanner, "scan_antigravity_tokens", empty),
            mock.patch.object(scanner, "scan_hermes_tokens", empty),
            mock.patch.object(scanner, "scan_workbuddy_tokens", return_value=[event]),
        ]
        for patcher in patches:
            patcher.start()
            self.addCleanup(patcher.stop)

        history = scanner.get_historical_usage(1)
        heatmap = scanner.get_heatmap_data(1)

        self.assertEqual(history["values"], [120])
        self.assertEqual(heatmap["days"][0]["tokens"], 120)
        self.assertEqual(history["by_tool"]["WorkBuddy"], [120])

    def test_hermes_uses_end_time_and_includes_cache_write(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "state.db")
            conn = sqlite3.connect(path)
            conn.execute("""CREATE TABLE sessions (
                id TEXT, started_at REAL, ended_at REAL, model TEXT,
                input_tokens INTEGER, output_tokens INTEGER,
                cache_read_tokens INTEGER, cache_write_tokens INTEGER
            )""")
            conn.execute(
                "INSERT INTO sessions VALUES ('s1', ?, ?, 'glm-test', 100, 20, 300, 40)",
                (1_700_000_000, 1_800_000_000),
            )
            conn.commit()
            conn.close()

            with mock.patch.object(scanner, "HERMES_DB_PATH", path):
                events = scanner.scan_hermes_tokens(1_799_999_000)

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["timestamp"], 1_800_000_000)
        self.assertEqual(events[0]["total_tokens"], 460)
        self.assertEqual(events[0]["input_cached"], 300)
        self.assertEqual(events[0]["input_uncached"], 140)


if __name__ == "__main__":
    unittest.main()
