import json
import os
import sqlite3
import tempfile
import unittest
from unittest import mock

import scanner


class CodexScannerTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.db_path = os.path.join(self.temp_dir.name, "logs_2.sqlite")
        self.sessions_dir = os.path.join(self.temp_dir.name, "sessions")
        self.archived_dir = os.path.join(self.temp_dir.name, "archived_sessions")
        self.patchers = [
            mock.patch.object(scanner, "CODEX_LOG_DB_PATH", self.db_path),
            mock.patch.object(scanner, "CODEX_SESSIONS_DIR", self.sessions_dir),
            mock.patch.object(scanner, "CODEX_ARCHIVED_SESSIONS_DIR", self.archived_dir),
            mock.patch.object(scanner, "CC_SWITCH_DB_PATH", os.path.join(self.temp_dir.name, "missing-cc.db")),
            mock.patch.object(scanner, "HERMES_DB_PATH", os.path.join(self.temp_dir.name, "missing-hermes.db")),
            mock.patch.object(scanner, "WORKBUDDY_DB_PATH", os.path.join(self.temp_dir.name, "missing-workbuddy.db")),
            mock.patch.object(scanner, "WORKBUDDY_PROJECTS_DIR", os.path.join(self.temp_dir.name, "missing-workbuddy-projects")),
            mock.patch.object(scanner, "ANTIGRAVITY_STATS_PATH", os.path.join(self.temp_dir.name, "missing-antigravity.json")),
        ]
        for patcher in self.patchers:
            patcher.start()
            self.addCleanup(patcher.stop)

    def create_log_db(self, timestamp=1_800_000_000):
        conn = sqlite3.connect(self.db_path)
        conn.execute("""
            CREATE TABLE logs (
                id INTEGER PRIMARY KEY,
                ts INTEGER NOT NULL,
                ts_nanos INTEGER NOT NULL,
                target TEXT NOT NULL,
                feedback_log_body TEXT,
                thread_id TEXT
            )
        """)
        envelope = {
            "type": "response.completed",
            "response": {
                "id": "resp_test",
                "completed_at": timestamp,
                "model": "gpt-test",
                "usage": {
                    "input_tokens": 100,
                    "input_tokens_details": {"cached_tokens": 60},
                    "output_tokens": 20,
                    "total_tokens": 120,
                },
            },
        }
        conn.execute(
            "INSERT INTO logs(ts, ts_nanos, target, feedback_log_body, thread_id) VALUES (?, 0, ?, ?, ?)",
            (timestamp, "codex_api::sse::responses", "SSE event: " + json.dumps(envelope), "thread-test"),
        )
        conn.commit()
        conn.close()

    def test_reads_official_codex_sqlite_without_cc_switch(self):
        self.create_log_db()

        events = scanner.scan_codex_tokens(1_700_000_000)

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["tool"], "Codex")
        self.assertEqual(events[0]["model"], "gpt-test")
        self.assertEqual(events[0]["total_tokens"], 120)
        self.assertEqual(events[0]["input_cached"], 60)
        self.assertEqual(events[0]["session_id"], "thread-test")

    def test_today_usage_includes_codex_without_cc_switch(self):
        self.create_log_db()

        usage = scanner.get_today_usage()

        self.assertEqual(usage["summary"]["total_tokens"], 120)
        self.assertEqual(usage["by_tool"]["Codex"]["total_tokens"], 120)
        self.assertEqual(usage["by_model"]["gpt-test"], 120)

    def test_rollout_is_used_when_sqlite_is_missing(self):
        os.makedirs(self.sessions_dir)
        path = os.path.join(
            self.sessions_dir,
            "rollout-2026-01-15T00-00-00-019abcde-1234-5678-9abc-def012345678.jsonl",
        )
        rows = [
            {"timestamp": "2027-01-15T08:00:00Z", "type": "turn_context", "payload": {"model": "glm-test"}},
            {
                "timestamp": "2027-01-15T08:00:01Z",
                "type": "event_msg",
                "payload": {
                    "type": "token_count",
                    "info": {
                        "last_token_usage": {
                            "input_tokens": 70,
                            "cached_input_tokens": 30,
                            "output_tokens": 10,
                            "total_tokens": 80,
                        }
                    },
                },
            },
        ]
        with open(path, "w", encoding="utf-8") as stream:
            for row in rows:
                stream.write(json.dumps(row) + "\n")

        events = scanner.scan_codex_tokens(1_700_000_000)

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["model"], "glm-test")
        self.assertEqual(events[0]["input_cached"], 30)

    def test_dedup_prefers_cc_switch_model_across_bucket_boundary(self):
        cc_event = {
            "timestamp": 101,
            "total_tokens": 120,
            "model": "glm-provider",
        }
        codex_event = {
            "timestamp": 102,
            "total_tokens": 120,
            "model": "gpt-declared",
        }

        events = scanner._dedup_events([cc_event, codex_event])

        self.assertEqual(events, [cc_event])

    def test_rollout_skips_repeated_cumulative_usage(self):
        os.makedirs(self.sessions_dir)
        path = os.path.join(self.sessions_dir, "rollout-duplicate.jsonl")
        cumulative = {
            "input_tokens": 100,
            "cached_input_tokens": 60,
            "output_tokens": 20,
            "total_tokens": 120,
        }
        rows = [
            {
                "timestamp": "2027-01-15T08:00:01Z",
                "type": "event_msg",
                "payload": {"type": "token_count", "info": {
                    "total_token_usage": cumulative,
                    "last_token_usage": cumulative,
                }},
            },
            {
                "timestamp": "2027-01-15T08:00:10Z",
                "type": "event_msg",
                "payload": {"type": "token_count", "info": {
                    "total_token_usage": cumulative,
                    "last_token_usage": {"total_tokens": 999},
                }},
            },
        ]
        with open(path, "w", encoding="utf-8") as stream:
            for row in rows:
                stream.write(json.dumps(row) + "\n")

        events = scanner.scan_codex_tokens(1_700_000_000)

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["total_tokens"], 120)


if __name__ == "__main__":
    unittest.main()
