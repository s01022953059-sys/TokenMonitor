"""ZCode、MiniMax Code 与 Antigravity 数据源回归测试。

覆盖维度:
1. ZCode: 毫秒时间戳转秒、Anthropic 式缓存口径、status 过滤
2. MiniMax Code: ISO 时间戳转秒、message.usage 字段、只取 assistant message
3. Antigravity: antigravity-tools 代理库 unix 秒时间戳、warmup/0-token 过滤、
   cached⊆input 口径 (clamp)、WAL 缺 -shm 只读回退、五处聚合点接线
4. 跨源去重: 新工具与 cc-switch 不产生误合并
5. 社区上报: by_tool 动态包含新工具, today_tokens 含新工具用量
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
    """MiniMax Code 数据源:

    v2 主源: ~/.minimax/v2/sqlite/runtime-state.sqlite -> local_runtime_token_usage
    v1 兼容: ~/.pi/agent/sessions/**/*.jsonl

    SQLite 用例覆盖毫秒 ts 转秒 / mvs_ 前缀过滤 / cache 独立口径 /
    reasoning 不计入 total / 跨源去重 / 库缺失容错;JSONL 用例保留老路径回归。
    """

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.sessions_dir = os.path.join(self.temp_dir.name, "sessions", "proj")
        os.makedirs(self.sessions_dir)
        self.db_path = os.path.join(self.temp_dir.name, "runtime-state.sqlite")

        self.dir_patcher = mock.patch.object(
            scanner, "MINIMAX_SESSIONS_DIR",
            os.path.join(self.temp_dir.name, "sessions"),
        )
        self.db_patcher = mock.patch.object(scanner, "MINIMAX_DB_PATH", self.db_path)
        self.dir_patcher.start()
        self.db_patcher.start()
        self.addCleanup(self.dir_patcher.stop)
        self.addCleanup(self.db_patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    # --- helpers ---

    def _write_session(self, name, events):
        path = os.path.join(self.sessions_dir, name)
        with open(path, "w", encoding="utf-8") as stream:
            for event in events:
                stream.write(json.dumps(event) + "\n")
        return path

    def _make_minimax_db(self, rows):
        """rows: list[(turn_id, session_id, model, ts_ms, input, output,
                       reasoning, cache_read, cache_write)]"""
        conn = sqlite3.connect(self.db_path)
        conn.execute("""
            CREATE TABLE local_runtime_token_usage (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                session_id TEXT NOT NULL,
                agent_name TEXT,
                framework_type TEXT,
                turn_id TEXT,
                model TEXT,
                ts INTEGER NOT NULL,
                input_tokens INTEGER NOT NULL,
                output_tokens INTEGER NOT NULL,
                reasoning_tokens INTEGER NOT NULL DEFAULT 0,
                cache_read_tokens INTEGER NOT NULL DEFAULT 0,
                cache_write_tokens INTEGER NOT NULL DEFAULT 0,
                cost_usd REAL,
                raw TEXT
            )
        """)
        conn.executemany(
            """INSERT INTO local_runtime_token_usage
               (turn_id, session_id, model, ts, input_tokens, output_tokens,
                reasoning_tokens, cache_read_tokens, cache_write_tokens)
               VALUES (?,?,?,?,?,?,?,?,?)""",
            rows,
        )
        conn.commit()
        conn.close()

    # --- JSONL (v1 兜底) 用例 ---

    def test_jsonl_iso_timestamp_converted_to_seconds(self):
        """ISO 8601 字符串转成 unix 秒, 与其他源对齐。"""
        self._write_session("s1.jsonl", [
            {"type": "session", "timestamp": "2026-05-26T10:00:00.000Z"},
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        expected = int(datetime.datetime.fromisoformat(
            "2026-05-26T10:00:01.000+00:00").astimezone().timestamp())
        self.assertEqual(events[0]["timestamp"], expected)

    def test_jsonl_only_assistant_messages_counted(self):
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

    def test_jsonl_anthropic_cache_split(self):
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

    def test_jsonl_total_tokens_fallback_when_zero(self):
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

    def test_jsonl_skips_message_without_usage(self):
        """assistant message 但没有 usage 字段, 不应产生事件。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5"}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 0)

    def test_jsonl_end_timestamp_filters_range(self):
        """end_timestamp 参数正确过滤上界。"""
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
            {"type": "message", "timestamp": "2026-05-26T11:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 200, "output": 20, "totalTokens": 220}}},
        ])
        end_ts = int(datetime.datetime.fromisoformat(
            "2026-05-26T10:30:00.000+00:00").astimezone().timestamp())
        events = scanner.scan_minimax_tokens(1_700_000_000, end_ts)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["total_tokens"], 120)

    # --- SQLite (v2 主源) 用例 ---

    def test_sqlite_millisecond_timestamp_converted_to_seconds(self):
        """ts 列是毫秒, 返回的 timestamp 必须是秒。"""
        self._make_minimax_db([
            ("turn-1", "mvs_aaa", "custom_provider:zhipu-maas/glm-5.2",
             1_800_000_000_000, 100, 20, 0, 0, 0),
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["timestamp"], 1_800_000_000)
        self.assertEqual(events[0]["tool"], "MiniMax Code")
        self.assertEqual(events[0]["turn_id"], "turn-1")

    def test_sqlite_total_is_input_plus_output(self):
        """total_tokens = input + output, reasoning 不计入。"""
        self._make_minimax_db([
            ("t1", "mvs_aaa", "glm-5.2",
             1_800_000_000_000, 100, 20, 999, 0, 0),  # reasoning=999 必须被忽略
        ])
        e = scanner.scan_minimax_tokens(1_700_000_000)[0]
        self.assertEqual(e["input_uncached"], 100)
        self.assertEqual(e["input_cached"], 0)
        self.assertEqual(e["output_tokens"], 20)
        self.assertEqual(e["total_tokens"], 120)  # 不是 1119

    def test_sqlite_cache_split_into_input_cached(self):
        """cache_read + cache_write 计入 input_cached (Anthropic 式分离)。"""
        self._make_minimax_db([
            ("t1", "mvs_aaa", "glm-5.2",
             1_800_000_000_000, 100, 20, 0, 50, 30),
        ])
        e = scanner.scan_minimax_tokens(1_700_000_000)[0]
        self.assertEqual(e["input_uncached"], 100)
        self.assertEqual(e["input_cached"], 80)    # 50 + 30
        self.assertEqual(e["input_tokens"], 180)   # 100 + 80
        self.assertEqual(e["output_tokens"], 20)
        self.assertEqual(e["total_tokens"], 120)   # input_uncached + output

    def test_sqlite_session_id_mvs_prefix_filter(self):
        """只接 mvs_ 前缀的 session_id, 其他客户端写进同一张表的数据不混入。"""
        # 时间错开 > DEDUP_WINDOW_SECONDS, 避免被 _dedup_events 误合并
        self._make_minimax_db([
            ("t1", "mvs_aaaaaa", "glm-5.2", 1_800_000_000_000, 100, 20, 0, 0, 0),
            ("t2", "mvs_bbbbbb", "glm-5.2", 1_800_000_010_000, 100, 20, 0, 0, 0),
            ("t3", "other_xxx",  "glm-5.2", 1_800_000_020_000, 999, 99, 0, 0, 0),
            ("t4", "px_yyyy",    "glm-5.2", 1_800_000_030_000, 999, 99, 0, 0, 0),
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        ids = sorted(e["session_id"] for e in events)
        self.assertEqual(ids, ["mvs_aaaaaa", "mvs_bbbbbb"])

    def test_sqlite_skips_zero_total_rows(self):
        """input+output=0 的行不产生事件 (避免噪声)。"""
        self._make_minimax_db([
            ("t1", "mvs_aaa", "glm-5.2", 1_800_000_000_000, 0, 0, 0, 50, 30),
            ("t2", "mvs_bbb", "glm-5.2", 1_800_000_001_000, 100, 20, 0, 0, 0),
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["session_id"], "mvs_bbb")

    def test_sqlite_skips_rows_outside_time_window(self):
        """ts 不在 [start, end) 窗口的行被过滤。"""
        self._make_minimax_db([
            ("t1", "mvs_aaa", "glm-5.2", 1_600_000_000_000, 100, 20, 0, 0, 0),  # 太早
            ("t2", "mvs_bbb", "glm-5.2", 1_800_000_000_000, 100, 20, 0, 0, 0),
            ("t3", "mvs_ccc", "glm-5.2", 2_000_000_000_000, 100, 20, 0, 0, 0),  # 太晚
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000, 1_900_000_000)
        ids = sorted(e["session_id"] for e in events)
        self.assertEqual(ids, ["mvs_bbb"])

    def test_sqlite_missing_db_returns_empty(self):
        """SQLite 不存在时静默返回空 (旧 JSONL 路径仍生效)。"""
        if os.path.exists(self.db_path):
            os.remove(self.db_path)
        self._write_session("s1.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 100, "output": 20, "totalTokens": 120}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["turn_id"], "")

    def test_sqlite_missing_table_returns_empty(self):
        """数据库存在但 local_runtime_token_usage 表缺失 (升级中) 不崩。"""
        # 创建一个空库, 没有 local_runtime_token_usage 表
        conn = sqlite3.connect(self.db_path)
        conn.execute("CREATE TABLE other_stuff (id INTEGER)")
        conn.commit()
        conn.close()
        events = scanner.scan_minimax_tokens(1_700_000_000)
        self.assertEqual(events, [])

    # --- 跨源优先级用例 ---

    def test_merge_minimax_sources_dedupes_by_turn_id(self):
        """_merge_minimax_sources 按 turn_id 去重: SQLite 优先。"""
        sqlite_logs = [
            {"turn_id": "t1", "session_id": "mvs_aaa", "timestamp": 1800000000,
             "total_tokens": 150, "_source": "minimax_sqlite"},
        ]
        jsonl_logs = [
            # 同 turn_id 应被丢弃
            {"turn_id": "t1", "session_id": "mvs_aaa", "timestamp": 1800000000,
             "total_tokens": 999, "_source": "minimax_jsonl"},
            # 不同 turn_id 应保留
            {"turn_id": "t2", "session_id": "mvs_bbb", "timestamp": 1800000010,
             "total_tokens": 230, "_source": "minimax_jsonl"},
        ]
        merged = scanner._merge_minimax_sources(sqlite_logs, jsonl_logs)
        self.assertEqual(len(merged), 2)
        # SQLite 的 t1 必须保留 (不是 JSONL 的 999 那条)
        t1 = next(e for e in merged if e["turn_id"] == "t1")
        self.assertEqual(t1["_source"], "minimax_sqlite")
        self.assertEqual(t1["total_tokens"], 150)

    def test_merge_minimax_sources_falls_back_to_session_ts(self):
        """没有 turn_id 的旧 JSONL 事件用 (session_id, timestamp) 兜底去重。"""
        sqlite_logs = [
            {"turn_id": "t1", "session_id": "mvs_aaa", "timestamp": 1800000000,
             "total_tokens": 150, "_source": "minimax_sqlite"},
        ]
        jsonl_logs = [
            # 同 (session_id, timestamp) 应被丢弃
            {"turn_id": "", "session_id": "mvs_aaa", "timestamp": 1800000000,
             "total_tokens": 999, "_source": "minimax_jsonl"},
            # 不同 session_id 应保留
            {"turn_id": "", "session_id": "mvs_bbb", "timestamp": 1800000000,
             "total_tokens": 230, "_source": "minimax_jsonl"},
        ]
        merged = scanner._merge_minimax_sources(sqlite_logs, jsonl_logs)
        self.assertEqual(len(merged), 2)
        t1 = next(e for e in merged if e["session_id"] == "mvs_aaa")
        self.assertEqual(t1["_source"], "minimax_sqlite")
        self.assertEqual(t1["total_tokens"], 150)

    def test_jsonl_fills_in_turns_not_in_sqlite(self):
        """SQLite 没覆盖的 turn (旧版) 仍能从 JSONL 补上。"""
        # SQLite: 只 1 条
        self._make_minimax_db([
            ("t_sqlite", "mvs_aaa", "glm-5.2",
             1_800_000_000_000, 100, 50, 0, 0, 0),
        ])
        # JSONL: 不同的 turn (JSONL 无 turn_id, 用 session_id+timestamp 兜底)
        # 写到一个文件名 sid-jsonl-turn, 让 session_id 不撞 SQLite 的
        self._write_session("sid-jsonl-turn.jsonl", [
            {"type": "message", "timestamp": "2026-05-26T10:00:01.000Z",
             "message": {"role": "assistant", "model": "gpt-5.5",
                         "usage": {"input": 200, "output": 30, "totalTokens": 230}}},
        ])
        events = scanner.scan_minimax_tokens(1_700_000_000)
        totals = sorted(e["total_tokens"] for e in events)
        self.assertEqual(totals, [150, 230])

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


class NormalizeModelNameTests(unittest.TestCase):
    """normalize_model_name: 剥 provider 前缀, 折叠 cc-switch 噪声变体。

    1.5.12 新增: MiniMax Code 在 SQLite 里写的是 'custom_provider:<provider>/<model>',
       dashboard 之前直接展示这个长串很难看。这里剥前缀只留 <model>。
    """

    def test_strips_custom_provider_prefix(self):
        """custom_provider:<provider>/<model> -> <model>"""
        self.assertEqual(
            scanner.normalize_model_name("custom_provider:zhipu-maas/glm-5.2"),
            "glm-5.2",
        )
        self.assertEqual(
            scanner.normalize_model_name("custom_provider:opencode-go/kimi-k3"),
            "kimi-k3",
        )
        self.assertEqual(
            scanner.normalize_model_name("custom_provider:opencode-go/deepseek-v4-flash"),
            "deepseek-v4-flash",
        )

    def test_strips_custom_local_prefix(self):
        """custom-local:<model> -> <model>"""
        self.assertEqual(
            scanner.normalize_model_name("custom-local:MiniMax-M3"),
            "minimax-m3",
        )

    def test_custom_provider_prefix_is_lowercased_first(self):
        """大小写归一化在剥前缀之前: 'CUSTOM_PROVIDER:...' 也能正确剥。"""
        self.assertEqual(
            scanner.normalize_model_name("CUSTOM_PROVIDER:ZhIPu-MAAS/GLM-5.2"),
            "glm-5.2",
        )

    def test_custom_provider_prefix_without_slash_keeps_rest(self):
        """防御: custom_provider:<model> (无 provider/分隔) 不崩, 保留全部 rest。"""
        self.assertEqual(
            scanner.normalize_model_name("custom_provider:somemodel"),
            "somemodel",
        )

    def test_non_prefixed_models_unchanged(self):
        """没有前缀的 model (其他 Agent 写的) 不受影响。"""
        self.assertEqual(scanner.normalize_model_name("glm-5.2"), "glm-5.2")
        self.assertEqual(scanner.normalize_model_name("gpt-5.6"), "gpt-5.6")
        self.assertEqual(scanner.normalize_model_name("qwen3.6-plus"), "qwen3.6-plus")
        self.assertEqual(scanner.normalize_model_name("qwen3.6-Plus"), "qwen3.6-plus")
        self.assertEqual(scanner.normalize_model_name("qwen3.6-plus-2026-04-02"),
                         "qwen3.6-plus")
        self.assertEqual(scanner.normalize_model_name("qwen3.6-plus-vl"),
                         "qwen3.6-plus-vl")

    def test_empty_and_none_return_other(self):
        self.assertEqual(scanner.normalize_model_name(""), "Other")
        self.assertEqual(scanner.normalize_model_name(None), "Other")


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

    def test_today_usage_includes_deduplicated_tool_model_breakdown(self):
        """二级统计使用去重后的事件，并同时支持工具→模型与模型→工具。"""
        now = int(datetime.datetime.now().timestamp())
        codex_gpt = {
            "timestamp": now, "tool": "Codex", "model": "gpt-5.5",
            "input_tokens": 80, "output_tokens": 20, "total_tokens": 100,
            "input_cached": 0, "input_uncached": 80, "session_id": "codex-1",
        }
        duplicate = dict(codex_gpt, timestamp=now + 1, session_id="duplicate")
        codex_glm = {
            "timestamp": now + 10, "tool": "Codex", "model": "glm-5.2",
            "input_tokens": 160, "output_tokens": 40, "total_tokens": 200,
            "input_cached": 0, "input_uncached": 160, "session_id": "codex-2",
        }
        claude_gpt = {
            "timestamp": now + 20, "tool": "Claude", "model": "gpt-5.5",
            "input_tokens": 40, "output_tokens": 10, "total_tokens": 50,
            "input_cached": 0, "input_uncached": 40, "session_id": "claude-1",
        }
        empty = mock.Mock(return_value=[])
        with mock.patch.object(scanner, "scan_cc_switch_logs", return_value=[codex_gpt, codex_glm, claude_gpt]), \
             mock.patch.object(scanner, "scan_codex_tokens", return_value=[duplicate]), \
             mock.patch.object(scanner, "scan_antigravity_tokens", empty), \
             mock.patch.object(scanner, "scan_hermes_tokens", empty), \
             mock.patch.object(scanner, "scan_zcode_tokens", empty), \
             mock.patch.object(scanner, "scan_minimax_tokens", empty), \
             mock.patch.object(scanner, "scan_workbuddy_tokens", empty), \
             mock.patch.object(scanner, "get_deepseek_balance", return_value={}):
            usage = scanner.get_today_usage()

        self.assertEqual(usage["summary"]["total_tokens"], 350)
        self.assertEqual(usage["by_tool_model"]["Codex"], {
            "gpt-5.5": 100,
            "glm-5.2": 200,
        })
        self.assertEqual(usage["by_tool_model"]["Claude"], {"gpt-5.5": 50})


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


class SortOrderConsistencyTests(unittest.TestCase):
    """排序一致性与 Other 位置回归测试。

    确保 Mac (Python) 与 Win (Go) 两端:
    1. history 的 by_tool/by_model 里 Other 始终在末尾
    2. tool_distribution 按用量降序
    3. _dedup_events 稳定排序 (相同时间戳保持输入顺序)
    4. community _format_report_tools 按用量降序 + 名称升序 tiebreak
    """

    def test_history_by_tool_other_is_last(self):
        """get_historical_usage 的 by_tool key 列表里 Other 必须在末尾。"""
        hu = scanner.get_historical_usage(30)
        tools = list(hu["by_tool"].keys())
        if "Other" in tools:
            self.assertEqual(tools[-1], "Other",
                             f"Other not last in by_tool: {tools}")

    def test_history_by_model_other_is_last(self):
        """get_historical_usage 的 by_model key 列表里 Other 必须在末尾。"""
        hu = scanner.get_historical_usage(30)
        models = list(hu["by_model"].keys())
        if "Other" in models:
            self.assertEqual(models[-1], "Other",
                             f"Other not last in by_model: {models}")

    def test_tool_distribution_sorted_by_usage_desc(self):
        """community tool_distribution 应按占比降序排列。"""
        by_tool = {"Codex": 1000, "ZCode": 500, "Claude": 200, "Hermes": 50}
        # 模拟 community.py 的排序逻辑
        tool_distribution = {k: round(v / sum(by_tool.values()) * 100, 1)
                             for k, v in by_tool.items()}
        tool_distribution = dict(sorted(tool_distribution.items(),
                                        key=lambda x: -x[1]))
        keys = list(tool_distribution.keys())
        self.assertEqual(keys, ["Codex", "ZCode", "Claude", "Hermes"])

    def test_dedup_events_stable_for_same_timestamp(self):
        """相同时间戳的不同请求 (不同 total) 保持输入顺序。"""
        base_ts = 1800000000
        events = [
            {"timestamp": base_ts, "tool": "ZCode", "model": "glm-5.2",
             "input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
             "input_cached": 0, "input_uncached": 100, "session_id": "first"},
            {"timestamp": base_ts, "tool": "Codex", "model": "gpt-5.6",
             "input_tokens": 200, "output_tokens": 50, "total_tokens": 250,
             "input_cached": 0, "input_uncached": 200, "session_id": "second"},
        ]
        deduped = scanner._dedup_events(events)
        self.assertEqual(len(deduped), 2)
        # 稳定排序: 相同 timestamp 时 first 应在前
        self.assertEqual(deduped[0]["session_id"], "first")
        self.assertEqual(deduped[1]["session_id"], "second")

    def test_format_report_tools_tiebreak_by_name(self):
        """相同用量的工具按名称升序排列 (tiebreak)。"""
        by_tool = {"ZCode": 100, "Codex": 100, "MiniMax Code": 100}
        result = community._format_report_tools(by_tool)
        # 用量相同, 按名称升序: Codex + MiniMax Code + ZCode
        self.assertEqual(result, "Codex + MiniMax Code + ZCode")

    def test_format_report_tools_descending_by_usage(self):
        """工具列按用量降序, 不按字母序。"""
        by_tool = {"Hermes": 500, "Codex": 100, "ZCode": 1000}
        result = community._format_report_tools(by_tool)
        self.assertEqual(result, "ZCode + Hermes + Codex")


def _make_antigravity_db(path, rows, wal=False):
    """在 path 创建一个只含 token_usage 表的 Antigravity 代理 SQLite。

    rows 每行: (timestamp, account_email, model, input, output, total, cached)
    wal=True 时建库后切 WAL 并删除 -shm/-wal 伴生文件, 复现"代理未运行"场景。
    """
    conn = sqlite3.connect(path)
    conn.execute("""
        CREATE TABLE token_usage (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp INTEGER NOT NULL,
            account_email TEXT NOT NULL,
            model TEXT NOT NULL,
            input_tokens INTEGER NOT NULL DEFAULT 0,
            output_tokens INTEGER NOT NULL DEFAULT 0,
            total_tokens INTEGER NOT NULL DEFAULT 0,
            cached_tokens INTEGER NOT NULL DEFAULT 0
        )
    """)
    conn.executemany(
        "INSERT INTO token_usage (timestamp, account_email, model,"
        " input_tokens, output_tokens, total_tokens, cached_tokens)"
        " VALUES (?,?,?,?,?,?,?)", rows
    )
    if wal:
        conn.execute("PRAGMA journal_mode=WAL")
    conn.commit()
    conn.close()
    if wal:
        for ext in ("-shm", "-wal"):
            try:
                os.remove(path + ext)
            except OSError:
                pass


class AntigravityScannerTests(unittest.TestCase):
    """Antigravity (antigravity-tools 代理库) 数据源。

    与 go_build/antigravity_sqlite_test.go 是同口径的双端回归。
    """

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.db_path = os.path.join(self.temp_dir.name, "token_stats.db")
        self.patcher = mock.patch.object(scanner, "ANTIGRAVITY_DB_PATH", self.db_path)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)

    def test_warmup_zero_token_rows_skipped(self):
        """warmup 保活记录 (全 0 token) 不产出事件。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "gemini-3-pro-high", 1000, 200, 1200, 300),
            (now - 200, "a@b.c", "gemini-3.6-flash-medium", 0, 0, 0, 0),
            (now - 100, "a@b.c", "gemini-pro-agent", 0, 0, 0, 0),
        ])
        events = scanner.scan_antigravity_tokens(now - 3600)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["tool"], "Antigravity")
        self.assertEqual(events[0]["total_tokens"], 1200)

    def test_unix_second_timestamp_and_start_window(self):
        """timestamp 是 unix 秒原样透传; start 窗口过滤生效。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 7200, "a@b.c", "gemini-3-pro-high", 10, 5, 15, 0),  # 窗口外
            (now - 3600, "a@b.c", "gemini-3-pro-high", 20, 5, 25, 0),  # 窗口内
        ])
        events = scanner.scan_antigravity_tokens(now - 5400)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["timestamp"], now - 3600)
        self.assertLess(events[0]["timestamp"], 2_000_000_000)

    def test_end_timestamp_window(self):
        """end_timestamp 半开区间 (< end) 过滤生效, 供热力图详情使用。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "gemini-3-pro-high", 10, 5, 15, 0),
            (now - 100, "a@b.c", "gemini-3-pro-high", 20, 5, 25, 0),
        ])
        events = scanner.scan_antigravity_tokens(now - 3600, end_timestamp=now - 200)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["timestamp"], now - 300)

    def test_cached_clamped_to_input(self):
        """cached⊆input (Gemini 风格): cached>input 时 clamp, uncached 不为负。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "claude-opus-4-6-thinking", 500, 100, 600, 900),
            (now - 200, "a@b.c", "gemini-3-pro-high", 1000, 200, 1200, 300),
        ])
        events = scanner.scan_antigravity_tokens(now - 3600)
        self.assertEqual(len(events), 2)
        clamped = events[0]
        self.assertEqual(clamped["input_cached"], 500)
        self.assertEqual(clamped["input_uncached"], 0)
        normal = events[1]
        self.assertEqual(normal["input_cached"], 300)
        self.assertEqual(normal["input_uncached"], 700)
        self.assertEqual(normal["input_tokens"], 1000)

    def test_model_normalized(self):
        """模型名小写化 + 日期后缀剥掉 (与 normalize_model_name 对齐)。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "Gemini-3-Pro-High-2026-01-01", 100, 10, 110, 0),
            (now - 200, "a@b.c", "CLAUDE-SONNET-4-6", 100, 10, 110, 0),
        ])
        events = scanner.scan_antigravity_tokens(now - 3600)
        self.assertEqual({e["model"] for e in events},
                         {"gemini-3-pro-high", "claude-sonnet-4-6"})

    def test_missing_db_returns_empty(self):
        """代理未安装 (DB 不存在) 时静默返回 []。"""
        events = scanner.scan_antigravity_tokens(1_700_000_000)
        self.assertEqual(events, [])

    def test_missing_table_returns_empty(self):
        """旧版 schema 缺 token_usage 表时静默返回 [], 不抛异常。"""
        conn = sqlite3.connect(self.db_path)
        conn.execute("CREATE TABLE accounts (id INTEGER PRIMARY KEY)")
        conn.commit()
        conn.close()
        events = scanner.scan_antigravity_tokens(1_700_000_000)
        self.assertEqual(events, [])

    def test_wal_without_shm_still_readable(self):
        """WAL 库在代理未运行 (-shm 缺失) 时经 immutable 回退仍可读。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "gemini-3-pro-high", 1000, 200, 1200, 300),
        ], wal=True)
        self.assertFalse(os.path.exists(self.db_path + "-shm"))
        events = scanner.scan_antigravity_tokens(now - 3600)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["total_tokens"], 1200)
        # 只读原则: 扫描不得在用户目录留下 -shm/-wal 副产物
        self.assertFalse(os.path.exists(self.db_path + "-shm"))
        self.assertFalse(os.path.exists(self.db_path + "-wal"))

    def test_aggregations_include_antigravity(self):
        """五处聚合点接线: 今日/历史/会话列表/热力图都包含 Antigravity 事件。"""
        now = int(datetime.datetime.now().timestamp())
        _make_antigravity_db(self.db_path, [
            (now - 300, "a@b.c", "gemini-3-pro-high", 1000, 200, 1200, 300),
            (now - 200, "a@b.c", "gemini-3.6-flash-medium", 0, 0, 0, 0),
        ])
        missing = os.path.join(self.temp_dir.name, "missing")
        path_consts = [
            "CC_SWITCH_DB_PATH", "CODEX_LOG_DB_PATH", "CODEX_SESSIONS_DIR",
            "CODEX_ARCHIVED_SESSIONS_DIR", "HERMES_DB_PATH", "ZCODE_DB_PATH",
            "MINIMAX_DB_PATH", "MINIMAX_SESSIONS_DIR", "WORKBUDDY_DB_PATH",
            "WORKBUDDY_PROJECTS_DIR", "CLAUDE_PROJECTS_DIR",
        ]
        with mock.patch.multiple(scanner, **{name: missing for name in path_consts}), \
             mock.patch.object(scanner, "get_deepseek_balance",
                               return_value={"balance": "0.00", "currency": "CNY", "status": "Offline"}):
            today = scanner.get_today_usage()
            self.assertIn("Antigravity", today["by_tool"])
            self.assertEqual(today["by_tool"]["Antigravity"]["total_tokens"], 1200)

            history = scanner.get_historical_usage(1)
            self.assertIn("Antigravity", history["by_tool"])
            self.assertEqual(history["by_tool"]["Antigravity"][-1], 1200)

            sessions = scanner.get_session_list(days=1)
            self.assertTrue(any(s["tool"] == "Antigravity" and s["total_tokens"] == 1200
                                for s in sessions["sessions"]))

            heatmap = scanner.get_heatmap_data(1)
            today_str = datetime.datetime.now().strftime("%Y-%m-%d")
            day = next(d for d in heatmap["days"] if d["date"] == today_str)
            self.assertEqual(day["tokens"], 1200)


if __name__ == "__main__":
    unittest.main()
