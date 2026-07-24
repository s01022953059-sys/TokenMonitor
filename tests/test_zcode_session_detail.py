"""ZCode 会话详情单元测试 (追补修复 c4fca0b "修复 ZCode 会话详情看不到的问题")。

覆盖维度:
1. happy path: 正常 session + 正常 message + 正常 text part → 返回完整 messages
2. 边界: 数据库文件缺失 → 不崩 + detail_source='zcode' + 空
3. 边界: session 在 DB 里不存在 → 不崩 + detail_source='zcode' + 空
4. 分页: 多页查询 total / total_pages / 切片一致
5. 内容过滤: 同 message_id 下多种 part 类型 (text / tool / step-finish) 仅取 text
6. 角色过滤: system 等非 user/assistant 角色不出现

调用层为公开 API scanner.get_session_detail(..., tool='ZCode'), 与 server.py / 前端一致。
"""
import json
import os
import sqlite3
import tempfile
import unittest
from unittest import mock

import scanner


def _make_zcode_session_db(path, sessions):
    """在 path 创建一个含 session / message / part 三表的 ZCode SQLite。

    sessions 参数是 [{ id, messages: [{ role, created_ms, parts: [{ type, text? }] }] }, ...]
    与项目里 Go 端 writeZCodeDBFixture (go_build/session_detail_test.go) schema 对齐。
    """
    conn = sqlite3.connect(path)
    conn.executescript("""
        CREATE TABLE session (
            id text PRIMARY KEY,
            project_id text NOT NULL,
            slug text NOT NULL,
            directory text NOT NULL,
            title text NOT NULL,
            version text NOT NULL,
            time_created integer NOT NULL,
            time_updated integer NOT NULL
        );
        CREATE TABLE message (
            id text PRIMARY KEY,
            session_id text NOT NULL,
            time_created integer NOT NULL,
            time_updated integer NOT NULL,
            data text NOT NULL
        );
        CREATE TABLE part (
            id text PRIMARY KEY,
            message_id text NOT NULL,
            session_id text NOT NULL,
            time_created integer NOT NULL,
            time_updated integer NOT NULL,
            data text NOT NULL
        );
    """)
    for session in sessions:
        conn.execute(
            "INSERT INTO session VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            (session["id"], "proj", session["id"], "/tmp", session.get("title", "title"), "v1",
             session.get("created_ms", 1780000000000), session.get("created_ms", 1780000000000)),
        )
        for msg_index, msg in enumerate(session.get("messages", [])):
            msg_id = msg.get("id") or f"msg-{session['id']}-{msg_index}"
            msg_data = json.dumps({"role": msg["role"], "time": {"created": msg["created_ms"]}})
            conn.execute(
                "INSERT INTO message VALUES (?, ?, ?, ?, ?)",
                (msg_id, session["id"], msg["created_ms"], msg["created_ms"], msg_data),
            )
            for part_index, part in enumerate(msg.get("parts", [])):
                part_id = f"{msg_id}-p{part_index}"
                part_data = json.dumps({"type": part.get("type", "text"), "text": part.get("text", "")})
                conn.execute(
                    "INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)",
                    (part_id, msg_id, session["id"], msg["created_ms"] + part_index,
                     msg["created_ms"] + part_index, part_data),
                )
    conn.commit()
    conn.close()
    return path


class TestZCodeSessionDetail(unittest.TestCase):
    """scanner.get_session_detail(tool='ZCode') 公开 API 全路径覆盖。"""

    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.db_path = os.path.join(self.temp_dir.name, ".zcode", "cli", "db", "db.sqlite")
        os.makedirs(os.path.dirname(self.db_path), exist_ok=True)
        self.patcher = mock.patch.object(scanner, "ZCODE_DB_PATH", self.db_path)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)
        self.addCleanup(self.temp_dir.cleanup)

    def test_zcode_session_detail_reads_message_content(self):
        """happy path: 1 session + 2 message (user/assistant) + 各 1 text part,
        应返回 detail_source='zcode' + 2 条消息按 time_created 升序, role/text/timestamp 正确。"""
        _make_zcode_session_db(self.db_path, [{
            "id": "sess-test-001",
            "created_ms": 1780000000000,
            "messages": [
                {"id": "msg-u", "role": "user", "created_ms": 1780000001000,
                 "parts": [{"type": "text", "text": "hello zcode"}]},
                {"id": "msg-a", "role": "assistant", "created_ms": 1780000002000,
                 "parts": [{"type": "text", "text": "world zcode"}]},
            ],
        }])
        detail = scanner.get_session_detail("sess-test-001", page=1, page_size=20, tool="ZCode")
        self.assertEqual(detail["detail_source"], "zcode")
        self.assertEqual(detail["session_id"], "sess-test-001")
        self.assertEqual(detail["total"], 2)
        self.assertEqual(detail["page"], 1)
        self.assertEqual(detail["page_size"], 20)
        self.assertEqual(detail["total_pages"], 1)
        self.assertEqual(len(detail["messages"]), 2)
        self.assertEqual(detail["messages"][0]["role"], "user")
        self.assertEqual(detail["messages"][0]["text"], "hello zcode")
        self.assertEqual(detail["messages"][1]["role"], "assistant")
        self.assertEqual(detail["messages"][1]["text"], "world zcode")
        # timestamp 应是 YYYY-MM-DD HH:MM:SS 格式
        import re
        ts_pattern = re.compile(r"^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$")
        self.assertRegex(detail["messages"][0]["timestamp"], ts_pattern)

    def test_zcode_session_detail_db_missing_returns_empty_zcode(self):
        """DB 文件不存在 → 不崩, 返回 detail_source='zcode' + 总数 0。"""
        # 不调用 _make_zcode_session_db, 故意保持 DB 不存在
        detail = scanner.get_session_detail("any-id", page=1, page_size=20, tool="ZCode")
        self.assertEqual(detail["detail_source"], "zcode")
        self.assertEqual(detail["session_id"], "any-id")
        self.assertEqual(detail["total"], 0)
        self.assertEqual(detail["messages"], [])
        self.assertEqual(detail["total_pages"], 1)

    def test_zcode_session_detail_session_not_found_returns_empty_zcode(self):
        """DB 存在但 session 表里没有目标 id → detail_source='zcode' + 总数 0。"""
        _make_zcode_session_db(self.db_path, [{
            "id": "other-session-id",
            "created_ms": 1780000000000,
        }])
        detail = scanner.get_session_detail("missing-session-id", page=1, page_size=20, tool="ZCode")
        self.assertEqual(detail["detail_source"], "zcode")
        self.assertEqual(detail["total"], 0)
        self.assertEqual(detail["messages"], [])
        self.assertEqual(detail["total_pages"], 1)

    def test_zcode_session_detail_pagination_and_totals(self):
        """5 条 message, page_size=2 → total=5 不变, 3 页切片正确。"""
        messages = [
            {"id": f"msg-{i}", "role": "user" if i % 2 == 0 else "assistant",
             "created_ms": 1780000010000 + i * 1000,
             "parts": [{"type": "text", "text": f"msg{i} body"}]}
            for i in range(5)
        ]
        _make_zcode_session_db(self.db_path, [{
            "id": "sess-page",
            "created_ms": 1780000000000,
            "messages": messages,
        }])

        page1 = scanner.get_session_detail("sess-page", page=1, page_size=2, tool="ZCode")
        page2 = scanner.get_session_detail("sess-page", page=2, page_size=2, tool="ZCode")
        page3 = scanner.get_session_detail("sess-page", page=3, page_size=2, tool="ZCode")

        for page in (page1, page2, page3):
            self.assertEqual(page["detail_source"], "zcode")
            self.assertEqual(page["total"], 5)
            self.assertEqual(page["total_pages"], 3)

        self.assertEqual(len(page1["messages"]), 2)
        self.assertEqual(page1["messages"][0]["text"], "msg0 body")
        self.assertEqual(page1["messages"][1]["text"], "msg1 body")

        self.assertEqual(len(page2["messages"]), 2)
        self.assertEqual(page2["messages"][0]["text"], "msg2 body")
        self.assertEqual(page2["messages"][1]["text"], "msg3 body")

        self.assertEqual(len(page3["messages"]), 1)
        self.assertEqual(page3["messages"][0]["text"], "msg4 body")

    def test_zcode_session_detail_skips_non_text_parts(self):
        """同 message_id 下同时存在 text / tool / step-finish / timeline parts,
        正文只拼接 type='text' 的内容, 工具结果不混入。"""
        _make_zcode_session_db(self.db_path, [{
            "id": "sess-parts",
            "created_ms": 1780000000000,
            "messages": [{
                "id": "msg-mix",
                "role": "assistant",
                "created_ms": 1780000001000,
                "parts": [
                    {"type": "text", "text": "visible-text\n"},
                    {"type": "tool", "text": "should-not-appear\n"},
                    {"type": "step-finish", "text": "should-not-appear\n"},
                    {"type": "timeline", "text": "should-not-appear\n"},
                    {"type": "text", "text": "second-text"},
                ],
            }],
        }])
        detail = scanner.get_session_detail("sess-parts", page=1, page_size=20, tool="ZCode")
        self.assertEqual(detail["total"], 1)
        text = detail["messages"][0]["text"]
        self.assertIn("visible-text", text)
        self.assertIn("second-text", text)
        self.assertNotIn("should-not-appear", text)

    def test_zcode_session_detail_skips_non_user_assistant_roles(self):
        """role=system (或非 user/assistant) 的 message 不应出现在结果中。"""
        _make_zcode_session_db(self.db_path, [{
            "id": "sess-roles",
            "created_ms": 1780000000000,
            "messages": [
                {"id": "msg-sys", "role": "system", "created_ms": 1780000001000,
                 "parts": [{"type": "text", "text": "system-prompt-body"}]},
                {"id": "msg-step", "role": "step-start", "created_ms": 1780000001500,
                 "parts": [{"type": "text", "text": "step-start-body"}]},
                {"id": "msg-user", "role": "user", "created_ms": 1780000002000,
                 "parts": [{"type": "text", "text": "real-user-body"}]},
                {"id": "msg-asst", "role": "assistant", "created_ms": 1780000003000,
                 "parts": [{"type": "text", "text": "real-assistant-body"}]},
            ],
        }])
        detail = scanner.get_session_detail("sess-roles", page=1, page_size=20, tool="ZCode")
        self.assertEqual(detail["total"], 2)
        roles = [m["role"] for m in detail["messages"]]
        self.assertEqual(roles, ["user", "assistant"])
        texts = [m["text"] for m in detail["messages"]]
        self.assertIn("real-user-body", texts)
        self.assertIn("real-assistant-body", texts)
        self.assertNotIn("system-prompt-body", texts)
        self.assertNotIn("step-start-body", texts)


if __name__ == "__main__":
    unittest.main()
