import datetime
import threading
import time
import os
import tempfile
import unittest
from unittest import mock

import community


class CommunityTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)

        self.community_dir = self.temp_dir.name
        self.user_id_file = os.path.join(self.community_dir, "community_id.txt")
        self.optin_file = os.path.join(self.community_dir, "community_optin.txt")
        self.credential_file = os.path.join(self.community_dir, "community_credential.json")
        self.group_code_file = os.path.join(self.community_dir, "group_code.txt")
        self.group_created_file = os.path.join(self.community_dir, "created_groups.txt")
        self.group_name_cache_file = os.path.join(self.community_dir, "group_names.json")
        with open(self.user_id_file, "w") as f:
            f.write("User_TEST1")
        with open(self.optin_file, "w") as f:
            f.write("true")

        for name, value in (
            ("COMMUNITY_DIR", self.community_dir),
            ("USER_ID_FILE", self.user_id_file),
            ("OPTIN_FILE", self.optin_file),
            ("CREDENTIAL_FILE", self.credential_file),
            ("GROUP_CODE_FILE", self.group_code_file),
            ("GROUP_CREATED_FILE", self.group_created_file),
            ("GROUP_NAME_CACHE_FILE", self.group_name_cache_file),
        ):
            patcher = mock.patch.object(community, name, value)
            patcher.start()
            self.addCleanup(patcher.stop)

        community._aggregate_cache["data"] = None
        community._aggregate_cache["ts"] = 0
        if hasattr(community, "_history_cache"):
            community._history_cache.clear()

    def test_new_user_id_is_always_eight_characters(self):
        self.assertRegex(community._new_user_id(), r"^User_[A-Z0-9]{8}$")

    def test_rank_history_counts_all_participants_but_returns_top_ten_series(self):
        snapshots = []
        for day in ("2026-07-26", "2026-07-27"):
            participants = [
                {
                    "id": f"User_{index:02d}",
                    "display_name": f"用户{index:02d}",
                    "tokens": (12 - index) * (100 if day.endswith("26") else 200),
                }
                for index in range(12)
            ]
            snapshots.append({"date": day, "participants": participants, "leaderboard": participants[:10]})

        result = community._build_rank_history_series(snapshots)

        self.assertEqual(result["participant_count"], 12)
        self.assertTrue(result["participant_count_complete"])
        self.assertEqual(result["dates"], ["2026-07-26", "2026-07-27"])
        self.assertEqual(len(result["series"]), 10)
        self.assertEqual(result["series"][0]["id"], "User_00")
        self.assertEqual(result["series"][0]["ranks"], [1, 1])
        self.assertNotIn("User_11", [item["id"] for item in result["series"]])

        legacy = community._build_rank_history_series([{"date": "2026-07-25", "leaderboard": snapshots[0]["leaderboard"]}])
        self.assertFalse(legacy["participant_count_complete"])

    def test_rank_history_calendar_period_bounds(self):
        today = datetime.date(2026, 5, 15)
        expected = {
            "week": ("2026-05-11", "2026-05-15"),
            "month": ("2026-05-01", "2026-05-15"),
            "quarter": ("2026-04-01", "2026-05-15"),
            "year": ("2026-01-01", "2026-05-15"),
        }
        for period, bounds in expected.items():
            with self.subTest(period=period):
                start, end = community._community_history_date_bounds(period, today)
                self.assertEqual((start.isoformat(), end.isoformat()), bounds)

    def test_rank_history_keeps_zero_usage_days_ranked_by_prior_history(self):
        snapshots = [
            {
                "date": "2026-07-26",
                "participants": [
                    {"id": "User_A", "display_name": "A", "tokens": 1000},
                    {"id": "User_B", "display_name": "B", "tokens": 10},
                ],
            },
            {
                "date": "2026-07-27",
                "participants": [
                    {"id": "User_A", "display_name": "A", "tokens": 1},
                    {"id": "User_B", "display_name": "B", "tokens": 20},
                    {"id": "User_C", "display_name": "C", "tokens": 2000},
                ],
            },
            {"date": "2026-07-28", "participants": []},
        ]

        result = community._build_rank_history_series(snapshots)
        by_id = {item["id"]: item for item in result["series"]}

        self.assertEqual(by_id["User_A"]["tokens"], [1000, 1, 0])
        self.assertEqual(by_id["User_B"]["tokens"], [10, 20, 0])
        self.assertEqual(by_id["User_C"]["tokens"], [0, 2000, 0])
        self.assertEqual(by_id["User_A"]["ranks"], [1, 3, 2])
        self.assertEqual(by_id["User_B"]["ranks"], [2, 2, 3])
        self.assertEqual(by_id["User_C"]["ranks"], [3, 1, 1])
        self.assertTrue(all(rank > 0 for item in result["series"] for rank in item["ranks"]))

    def test_external_community_requests_follow_system_proxy(self):
        request = community.urllib.request.Request("https://community.example.test/report")
        response = mock.MagicMock()
        opener = mock.MagicMock()
        opener.open.return_value = response
        with mock.patch.object(community.urllib.request, "getproxies", return_value={"https": "http://127.0.0.1:7890"}), mock.patch.object(
            community.urllib.request, "build_opener", return_value=opener
        ) as build_opener:
            self.assertIs(community._open_external_request(request, 20), response)

        proxy_handler = build_opener.call_args.args[0]
        self.assertEqual(proxy_handler.proxies["https"], "http://127.0.0.1:7890")
        opener.open.assert_called_once_with(request, timeout=20)

    def test_legacy_opt_out_is_migrated_to_automatic_membership(self):
        with open(self.optin_file, "w") as stream:
            stream.write("false")

        self.assertTrue(community.is_opted_in())
        community.set_optin(False)
        with open(self.optin_file, "r") as stream:
            self.assertEqual(stream.read(), "true")

    def test_report_uses_relay_without_gitcode_credentials(self):
        usage = {
            "summary": {"date": "2026-07-10", "total_tokens": 26_391_088},
            "by_tool": {"Codex": {"total_tokens": 20_000_000}},
        }
        relay_calls = []

        def fake_relay(report):
            relay_calls.append(report)
            return {"ok": True, "status": "synced", "message": "匿名统计已同步", "reported_at": "2026-07-10T08:00:00Z"}

        with mock.patch.object(community, "_relay_request", side_effect=fake_relay):
            result = community.report_community_stats(usage)

        self.assertTrue(result["ok"])
        self.assertEqual(result["status"], "synced")
        self.assertEqual(len(relay_calls), 1)
        report = relay_calls[0]
        self.assertEqual(report["id"], "User_TEST1")
        self.assertEqual(len(report["device_secret"]), 43)
        self.assertEqual(report["today_tokens"], 26_391_088)
        self.assertEqual(report["report_date"], "2026-07-10")

    def test_profile_update_uses_local_credential_and_clears_cache(self):
        captured = []
        community._aggregate_cache.update(data={"cached": True}, ts=123)

        def fake_profile(payload):
            captured.append(payload)
            return {"ok": True, "status": "updated", "display_name": "鹏帅"}

        with mock.patch.object(community, "_profile_request", side_effect=fake_profile):
            result = community.update_community_profile("鹏帅")

        self.assertTrue(result["ok"])
        self.assertEqual(captured[0]["id"], "User_TEST1")
        self.assertEqual(len(captured[0]["device_secret"]), 43)
        self.assertEqual(captured[0]["display_name"], "鹏帅")
        self.assertIsNone(community._aggregate_cache["data"])

    def test_legacy_identity_is_rotated_and_retried(self):
        usage = {
            "summary": {"date": "2026-07-10", "total_tokens": 123},
            "by_tool": {},
        }
        calls = []

        def fake_relay(report):
            calls.append(dict(report))
            if len(calls) == 1:
                return {"ok": False, "status": "identity_upgrade_required", "message": "升级"}
            return {"ok": True, "status": "synced", "message": "已同步"}

        with mock.patch.object(community, "_relay_request", side_effect=fake_relay):
            result = community.report_community_stats(usage)

        self.assertTrue(result["ok"])
        self.assertEqual(len(calls), 2)
        self.assertNotEqual(calls[0]["id"], calls[1]["id"])
        self.assertEqual(calls[1]["replaces_id"], calls[0]["id"])
        self.assertEqual(community.get_user_id(), calls[1]["id"])

    def test_rank_is_calculated_beyond_top_ten(self):
        today = datetime.date.today().isoformat()
        reports = [
            {
                "id": f"User_{i:02d}",
                "updated_at": today + "T08:00:00Z",
                "report_date": today,
                "today_tokens": (20 - i) * 1_000,
                "by_tool": {"Codex": (20 - i) * 1_000},
            }
            for i in range(11)
        ]
        reports.append({
            "id": "User_TEST1",
            "updated_at": today + "T08:30:00Z",
            "report_date": today,
            "today_tokens": 1,
            "by_tool": {"WorkBuddy": 1},
        })
        files = [{"name": f"User_{i}.json", "download_url": f"https://example.test/{i}.json"} for i in range(len(reports))]
        by_url = {f["download_url"]: report for f, report in zip(files, reports)}

        def fake_api(method, path, data=None, token=None, require_auth=True):
            self.assertFalse(require_auth)
            return files

        def fake_read(url, token=None):
            return by_url[url], None

        with mock.patch.object(community, "_gitcode_api", side_effect=fake_api), \
             mock.patch.object(community, "_read_remote_json", side_effect=fake_read):
            result = community.get_community_stats()

        self.assertEqual(result["my_rank"], 12)
        self.assertEqual(result["rank_status"], "outside_top10")
        self.assertEqual(result["rank_total"], 12)
        self.assertEqual(len(result["leaderboard"]), 10)

    def test_leaderboard_shows_all_tools_used_by_member(self):
        today = datetime.date.today().isoformat()
        reports = [{
            "id": "User_MULTI01",
            "updated_at": today + "T08:00:00Z",
            "report_date": today,
            "today_tokens": 100,
            "by_tool": {"Claude": 40, "Codex": 60},
        }]
        files = [{"name": "User_MULTI01.json", "download_url": "https://example.test/multi.json"}]

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(reports[0], None)):
            result = community.get_community_stats(force_refresh=True)

        self.assertEqual(result["leaderboard"][0]["tool"], "Codex + Claude")

    def test_public_data_and_relay_work_without_gitcode_credentials(self):
        calls = []

        def fake_api(method, path, data=None, token=None, require_auth=True):
            calls.append((method, path, token, require_auth))
            return []

        with mock.patch.object(community, "_gitcode_api", side_effect=fake_api):
            result = community.get_community_stats()

        self.assertEqual(calls, [("GET", community.REPORTS_PATH, None, False)])
        self.assertNotIn("error", result)
        self.assertTrue(result["can_report"])
        self.assertEqual(result["rank_status"], "pending")

    def test_report_read_failures_are_not_rendered_as_zero(self):
        files = [{"name": "User_BROKEN.json", "download_url": "https://example.test/broken.json"}]

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(None, "HTTP 502")):
            result = community.get_community_stats()

        self.assertEqual(result["data_status"], "load_failed")
        self.assertIn("全部读取失败", result["error"])

    def test_legacy_identity_duplicate_is_not_counted_twice(self):
        today = datetime.date.today().isoformat()
        reports = [
            {"id": "User_OLD01", "report_date": today, "today_tokens": 30_490_000, "by_tool": {"Codex": 30_490_000}},
            {"id": "User_TEST1", "auth_hash": "a" * 64, "replaces_id": "User_OLD01", "display_name": "鹏帅", "report_date": today, "today_tokens": 31_000_000, "by_tool": {"Codex": 31_000_000}},
            {"id": "User_OTHER", "auth_hash": "b" * 64, "report_date": today, "today_tokens": 10, "by_tool": {"WorkBuddy": 10}},
        ]
        files = [{"name": f"{report['id']}.json", "download_url": f"https://example.test/{i}"} for i, report in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        self.assertEqual(result["total_users"], 2)
        self.assertEqual(result["today_active_users"], 2)
        self.assertEqual(result["all_reporters"], 2)
        self.assertEqual(result["total_tokens_today"], 31_000_010)
        self.assertEqual([item["id"] for item in result["leaderboard"]], ["User_TEST1", "User_OTHER"])
        self.assertEqual(result["leaderboard"][0]["display_name"], "鹏帅")
        self.assertEqual(result["my_display_name"], "鹏帅")
        self.assertEqual(result["member_names"], {"User_TEST1": "鹏帅"})

    def test_different_users_with_same_fingerprint_are_not_deduplicated(self):
        """v1.4.88 回归测试：不同用户的统计相同时不应被误删。

        旧版 _dedupe_legacy_identity_reports 用 (report_date, today_tokens, by_tool)
        做指纹匹配新旧身份，但这会导致两个不同用户恰好统计相同时，未认证一方被误删。
        修复后只通过 replaces_id 显式关联去重，指纹碰撞不再导致数据丢失。
        """
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            # 小昆: 无 auth_hash 的旧身份, 统计与 User_X 恰好相同
            {"id": "User_XIAOKUN", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}},
            # User_X: 有 auth_hash 的新身份, 统计恰好与小昆相同 (但并非同一个人)
            {"id": "User_XXXXXX", "auth_hash": "a" * 64, "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}},
            # 第三个用户，确保基本功能正常
            {"id": "User_OTHER1", "auth_hash": "b" * 64, "report_date": today, "today_tokens": 100, "by_tool": {"Codex": 100}},
        ]
        files = [{"name": f"{report['id']}.json", "download_url": f"https://example.test/{i}"} for i, report in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # 关键断言: 三个用户都应该被计入, 小昆不能因为指纹碰撞被误删
        self.assertEqual(result["total_users"], 3,
                         "不同用户即使统计相同也不应被去重")
        self.assertEqual(result["today_active_users"], 3)
        self.assertEqual(result["all_reporters"], 3)
        self.assertEqual(result["total_tokens_today"], 5000 + 5000 + 100)
        leaderboard_ids = [item["id"] for item in result["leaderboard"]]
        self.assertIn("User_XIAOKUN", leaderboard_ids,
                      "小昆的统计不应因指纹碰撞而丢失")
        self.assertIn("User_XXXXXX", leaderboard_ids)
        self.assertIn("User_OTHER1", leaderboard_ids)

    def test_group_codes_aggregate_per_group(self):
        """v1.5.01 测试：用户同时属于多个组时，Token 在所有组里都计入。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            {"id": "User_MULTI1", "report_date": today, "today_tokens": 3000, "by_tool": {"Claude": 3000}, "group_codes": ["11111", "22222"]},
            {"id": "User_GROUP1", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}, "group_codes": ["11111"]},
            {"id": "User_GROUP2", "report_date": today, "today_tokens": 2000, "by_tool": {"Claude": 2000}, "group_codes": ["22222"]},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # 两个组都应该出现，且 Token 各自正确聚合
        group_codes = {g["code"]: g for g in result["groups"]}
        self.assertIn("11111", group_codes)
        self.assertIn("22222", group_codes)
        # 11111 = User_MULTI1(3000) + User_GROUP1(5000) = 8000
        self.assertEqual(group_codes["11111"]["total_tokens"], 8000)
        self.assertEqual(group_codes["11111"]["member_count"], 2)
        # 22222 = User_MULTI1(3000) + User_GROUP2(2000) = 5000
        self.assertEqual(group_codes["22222"]["total_tokens"], 5000)
        self.assertEqual(group_codes["22222"]["member_count"], 2)

    def test_group_codes_handle_legacy_string_format(self):
        """v1.5.01 向后兼容：旧中继/旧客户端发送字符串格式 group_code 也能正确处理。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            # 新格式 (数组)
            {"id": "User_NEW", "report_date": today, "today_tokens": 1000, "by_tool": {"Claude": 1000}, "group_codes": ["33333"]},
            # 旧格式 (字符串)
            {"id": "User_OLD", "report_date": today, "today_tokens": 2000, "by_tool": {"Claude": 2000}, "group_code": "44444"},
            # 两种都没有
            {"id": "User_NONE", "report_date": today, "today_tokens": 3000, "by_tool": {"Claude": 3000}},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # 应该有两个组都正确识别
        group_codes = {g["code"]: g for g in result["groups"]}
        self.assertEqual(group_codes.get("33333", {}).get("total_tokens"), 1000)
        self.assertEqual(group_codes.get("44444", {}).get("total_tokens"), 2000)
        # User_NONE 不应该出现在任何组里
        self.assertEqual(len(result["groups"]), 2)

    def test_add_remove_group_codes_manages_local_list(self):
        """v1.5.06 测试：add_group_code 直接保存本地，不要求服务端校验。"""
        # 清空环境
        if os.path.exists(self.credential_file):
            os.remove(self.credential_file)

        # 初始状态：未加入任何组
        self.assertEqual(community.get_group_codes(), [])

        # 添加组码（不调中继，直接保存）
        result = community.add_group_code("12345")
        self.assertTrue(result["ok"])
        self.assertEqual(community.get_group_codes(), ["12345"])

        # 重复添加应该去重
        result = community.add_group_code("12345")
        self.assertTrue(result["ok"])
        self.assertTrue(result.get("already_member"))
        self.assertEqual(community.get_group_codes(), ["12345"])

        # 添加第二个
        community.add_group_code("67890")
        self.assertEqual(community.get_group_codes(), ["12345", "67890"])

        # 移除一个
        community.remove_group_code("12345")
        self.assertEqual(community.get_group_codes(), ["67890"])

        # 移除不存在的码不报错
        community.remove_group_code("99999")
        self.assertEqual(community.get_group_codes(), ["67890"])

    def test_add_group_code_works_without_relay(self):
        """v1.5.07 测试：add_group_code 完全不调网络，即使中继不可达也能加入。"""
        if os.path.exists(self.credential_file):
            os.remove(self.credential_file)

        # mock get_group_info 抛异常（模拟网络不可达）
        with mock.patch.object(community, "get_group_info",
                               side_effect=Exception("connection refused")):
            with mock.patch.object(community, "_lookup_group_name",
                               side_effect=Exception("connection refused")):
                result = community.add_group_code("90245")
        # 关键断言：即使网络完全不可达，加入仍然成功
        self.assertTrue(result["ok"])
        self.assertIn("90245", result["codes"])
        self.assertEqual(community.get_group_codes(), ["90245"])

    def test_leaderboard_includes_group_codes(self):
        """v1.5.07 测试：leaderboard 条目带 group_codes，前端按 Tab 筛选。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            {"id": "User_TEAM_A", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}, "group_codes": ["11111"]},
            {"id": "User_TEAM_B", "report_date": today, "today_tokens": 3000, "by_tool": {"Claude": 3000}, "group_codes": ["22222"]},
            {"id": "User_NO_GROUP", "report_date": today, "today_tokens": 1000, "by_tool": {"Claude": 1000}},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # leaderboard 条目必须带 group_codes
        for entry in result["leaderboard"]:
            self.assertIn("group_codes", entry)
            self.assertIsInstance(entry["group_codes"], list)
        # 验证具体值
        team_a = next(e for e in result["leaderboard"] if e["id"] == "User_TEAM_A")
        self.assertEqual(team_a["group_codes"], ["11111"])
        team_b = next(e for e in result["leaderboard"] if e["id"] == "User_TEAM_B")
        self.assertEqual(team_b["group_codes"], ["22222"])
        no_group = next(e for e in result["leaderboard"] if e["id"] == "User_NO_GROUP")
        self.assertEqual(no_group["group_codes"], [])

    def test_clear_all_group_codes_resets_everything(self):
        """v1.5.04 测试：清空全部会重置组码 + 创建记录。"""
        if os.path.exists(self.credential_file):
            os.remove(self.credential_file)

        community.add_group_code("11111")
        community.add_group_code("22222")

        self.assertEqual(len(community.get_group_codes()), 2)
        community._record_created_code("11111")
        self.assertIn("11111", community._load_created_codes())

        result = community.clear_all_group_codes()
        self.assertTrue(result["ok"])
        self.assertEqual(community.get_group_codes(), [])
        self.assertEqual(community._load_created_codes(), [])

    def test_report_includes_group_codes(self):
        """v1.5.06 测试：report_community_stats 上报时携带 group_codes。"""
        if os.path.exists(self.credential_file):
            os.remove(self.credential_file)

        community.add_group_code("11111")
        community.add_group_code("22222")

        captured = {}
        def fake_relay(report):
            captured["report"] = report
            return {"ok": True, "status": "synced", "message": "ok"}
        with mock.patch.object(community, "_relay_request", side_effect=fake_relay):
            usage = {
                "summary": {"date": community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat(), "total_tokens": 1000},
                "by_tool": {"Claude": {"total_tokens": 1000}},
            }
            community.report_community_stats(usage)

        self.assertEqual(captured["report"]["group_codes"], ["11111", "22222"])

    def test_my_groups_include_local_codes_pending_report(self):
        """v1.5.04 测试：本地加入但服务端未上报的组也要在 my_groups 出现，标 pending_report。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        # 服务端没有任何该组的报告
        reports = [
            {"id": "User_OTHER", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}, "group_codes": ["OTHER1"]},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        # 本地加入了一个服务端没有的组（v1.5.06: 不需要中继校验）
        community.add_group_code("LOCAL1")

        with mock.patch.object(community, "_lookup_group_name", return_value="本地新建组"), \
             mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # 关键断言：本地加入的组也要出现
        my_codes = {g["code"] for g in result["my_groups"]}
        self.assertIn("LOCAL1", my_codes)
        # 服务端有的 OTHER1 不在 my_groups（本地没加入）
        self.assertNotIn("OTHER1", my_codes)
        # 待同步标记
        local_group = next(g for g in result["my_groups"] if g["code"] == "LOCAL1")
        self.assertTrue(local_group.get("pending_report"))
        self.assertEqual(local_group["name"], "本地新建组")
        self.assertEqual(local_group["total_tokens"], 0)

    def test_my_groups_mark_creator_with_code(self):
        """v1.5.04 测试：创建者本地记录的组会带 is_creator=true，便于前端显示码。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            {"id": "User_ME", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}, "group_codes": ["11111"]},
            {"id": "User_OTHER", "report_date": today, "today_tokens": 3000, "by_tool": {"Claude": 3000}, "group_codes": ["22222"]},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        # 标记 11111 是我创建的，22222 不是
        community._record_created_code("11111")
        # v1.5.06: add_group_code 不需要中继校验
        community.add_group_code("11111")
        # 把本地列表设为只包含 11111（我创建的）
        with mock.patch.object(community, "_lookup_group_name",
                               side_effect=lambda code: {"11111": "我创建的组", "22222": "别人的组"}.get(code)):
            with mock.patch.object(community, "_gitcode_api", return_value=files), \
                 mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
                result = community.get_community_stats()

        my_groups = result["my_groups"]
        self.assertEqual(len(my_groups), 1)
        self.assertEqual(my_groups[0]["code"], "11111")
        self.assertEqual(my_groups[0]["name"], "我创建的组")
        self.assertTrue(my_groups[0]["is_creator"])
        # 全局 groups 里两个都在
        all_codes = {g["code"] for g in result["groups"]}
        self.assertEqual(all_codes, {"11111", "22222"})

    def test_no_group_code_no_group_aggregation(self):
        """没有组码的报告不参与组队聚合，但仍在公共池排行。"""
        today = community._community_today() if hasattr(community, '_community_today') else datetime.date.today().isoformat()
        reports = [
            {"id": "User_SOLO", "report_date": today, "today_tokens": 5000, "by_tool": {"Claude": 5000}},  # 无组
            {"id": "User_TEAM", "report_date": today, "today_tokens": 3000, "by_tool": {"Claude": 3000}, "group_codes": ["99999"]},
        ]
        files = [{"name": f"{r['id']}.json", "download_url": f"https://example.test/{i}"} for i, r in enumerate(reports)]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        # User_SOLO 在公共池排行
        self.assertEqual(result["total_tokens_today"], 8000)
        self.assertEqual(result["today_active_users"], 2)
        # 但组队聚合只有 99999
        self.assertEqual(len(result["groups"]), 1)
        self.assertEqual(result["groups"][0]["code"], "99999")
        self.assertEqual(result["groups"][0]["total_tokens"], 3000)

    def test_historical_reporter_remains_in_community_count(self):
        today = datetime.date.today()
        yesterday = (today - datetime.timedelta(days=1)).isoformat()
        reports = [
            {
                "id": "User_TEST1",
                "report_date": today.isoformat(),
                "today_tokens": 1_000,
                "by_tool": {"Codex": 1_000},
            },
            {
                "id": "User_OLDER",
                "report_date": yesterday,
                "today_tokens": 2_000,
                "by_tool": {"Claude": 2_000},
            },
            {
                "id": "User_IDLE",
                "report_date": today.isoformat(),
                "today_tokens": 0,
                "by_tool": {},
            },
        ]
        files = [
            {"name": f"{report['id']}.json", "download_url": f"https://example.test/{index}"}
            for index, report in enumerate(reports)
        ]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        self.assertEqual(result["all_reporters"], 3)
        self.assertEqual(result["total_users"], 3)
        self.assertEqual(result["today_active_users"], 1)
        self.assertEqual(result["rank_total"], 1)
        self.assertEqual([item["id"] for item in result["leaderboard"]], ["User_TEST1"])

    def test_duplicate_user_id_uses_only_its_latest_report(self):
        today = datetime.date.today().isoformat()
        reports = [
            {"id": "User_TEST1", "report_date": today, "updated_at": today + "T08:00:00Z", "today_tokens": 100, "by_tool": {"Codex": 100}},
            {"id": "User_TEST1", "report_date": today, "updated_at": today + "T09:00:00Z", "today_tokens": 200, "by_tool": {"Codex": 200}},
            {"id": "User_OTHER", "report_date": today, "updated_at": today + "T08:30:00Z", "today_tokens": 10, "by_tool": {"Claude": 10}},
        ]
        files = [{"name": f"report-{index}.json", "download_url": f"https://example.test/{index}"} for index in range(len(reports))]
        by_url = {item["download_url"]: report for item, report in zip(files, reports)}

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=lambda url, token=None: (by_url[url], None)):
            result = community.get_community_stats()

        self.assertEqual(result["total_users"], 2)
        self.assertEqual(result["today_active_users"], 2)
        self.assertEqual(result["total_tokens_today"], 210)
        self.assertEqual(result["leaderboard"][0]["tokens"], 200)

    def test_force_refresh_bypasses_five_minute_cache(self):
        community._aggregate_cache.update(data={"cached": True}, ts=time.time())
        with mock.patch.object(community, "_gitcode_api", return_value=[]):
            cached = community.get_community_stats()
            fresh = community.get_community_stats(force_refresh=True)

        self.assertTrue(cached["cached"])
        self.assertNotIn("cached", fresh)

    def test_report_files_are_read_with_bounded_concurrency(self):
        today = datetime.date.today().isoformat()
        files = [
            {"name": f"User_{index}.json", "download_url": f"https://example.test/{index}"}
            for index in range(12)
        ]
        active = 0
        peak = 0
        lock = threading.Lock()

        def slow_read(url, token=None):
            nonlocal active, peak
            with lock:
                active += 1
                peak = max(peak, active)
            time.sleep(0.01)
            with lock:
                active -= 1
            return ({
                "id": "User_" + url.rsplit("/", 1)[-1],
                "report_date": today,
                "today_tokens": 1,
                "by_tool": {"Codex": 1},
            }, None)

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=slow_read):
            result = community.get_community_stats(force_refresh=True)

        self.assertGreater(peak, 1)
        self.assertLessEqual(peak, 8)
        self.assertEqual(result["today_active_users"], 12)

    def test_history_archives_are_read_concurrently_in_date_order(self):
        files = [
            {
                "name": f"2026-07-{day:02d}.json",
                "download_url": f"https://example.test/{day}",
            }
            for day in range(1, 13)
        ]
        active = 0
        peak = 0
        lock = threading.Lock()

        def slow_read(url, token=None):
            nonlocal active, peak
            day = int(url.rsplit("/", 1)[-1])
            with lock:
                active += 1
                peak = max(peak, active)
            time.sleep(0.01)
            with lock:
                active -= 1
            if day == 6:
                return None, "HTTP 502"
            return {
                "date": f"2026-07-{day:02d}",
                "participants": [{"id": f"User_{day:02d}", "tokens": day}],
            }, None

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=slow_read):
            result = community.get_community_history(30)

        self.assertGreater(peak, 1)
        self.assertLessEqual(peak, 8)
        self.assertEqual(
            result["dates"],
            [f"2026-07-{day:02d}" for day in range(1, 13) if day != 6],
        )

    def test_history_natural_week_only_reads_archives_within_calendar_bounds(self):
        files = [
            {
                "name": f"2026-07-{day:02d}.json",
                "download_url": f"https://example.test/{day}",
            }
            for day in range(24, 31)
        ]

        def read_snapshot(url, token=None):
            day = int(url.rsplit("/", 1)[-1])
            return {
                "date": f"2026-07-{day:02d}",
                "participants": [{"id": f"User_{day:02d}", "tokens": day}],
            }, None

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", side_effect=read_snapshot) as read_call, \
             mock.patch.object(community, "get_community_stats", return_value={"leaderboard": []}):
            result = community.get_community_history(
                period="week",
                today=datetime.date(2026, 7, 30),
            )

        self.assertEqual(result["dates"], ["2026-07-27", "2026-07-28", "2026-07-29", "2026-07-30"])
        self.assertEqual(result["range_start"], "2026-07-27")
        self.assertEqual(result["range_end"], "2026-07-30")
        self.assertEqual(read_call.call_count, 4)

    def test_history_cache_avoids_reloading_archives_within_ttl(self):
        files = [{"name": "2026-07-28.json", "download_url": "https://example.test/28"}]
        snapshot = {"date": "2026-07-28", "participants": [{"id": "User_28", "tokens": 28}]}

        with mock.patch.object(community, "_gitcode_api", return_value=files) as list_call, \
             mock.patch.object(community, "_read_remote_json", return_value=(snapshot, None)) as read_call, \
             mock.patch.object(community, "get_community_stats", return_value={"leaderboard": []}):
            first = community.get_community_history(30)
            second = community.get_community_history(30)

        self.assertEqual(first["dates"], second["dates"])
        self.assertEqual(list_call.call_count, 1)
        self.assertEqual(read_call.call_count, 1)

    def test_history_appends_today_realtime_when_no_archive_today(self):
        """今天尚无归档时，用实时排行榜补一个今天的 snapshot（鹏帅要求）。"""
        files = [{"name": "2026-07-29.json", "download_url": "https://example.test/29"}]
        archive_snapshot = {"date": "2026-07-29", "participants": [{"id": "User_A", "tokens": 100}]}
        realtime_leaderboard = [
            {"id": "User_A", "display_name": "甲", "tokens": 50},
            {"id": "User_B", "display_name": "乙", "tokens": 30},
        ]

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(archive_snapshot, None)), \
             mock.patch.object(community, "get_community_stats", return_value={"leaderboard": realtime_leaderboard}):
            result = community.get_community_history(period="week", today=datetime.date(2026, 7, 30))

        # 归档只有 07-29，今天 07-30 无归档，应用实时排行榜补一个 07-30 的点
        self.assertIn("2026-07-30", result["dates"])
        self.assertEqual(result["dates"][-1], "2026-07-30")
        # User_B 今天有用量，应出现在 series 里（如果累计排进前10）
        series_ids = [s["id"] for s in result["series"]]
        self.assertIn("User_B", series_ids)

    def test_history_does_not_duplicate_today_when_archive_exists(self):
        """今天已有归档时，不重复追加实时 snapshot。"""
        files = [{"name": "2026-07-30.json", "download_url": "https://example.test/30"}]
        archive_snapshot = {"date": "2026-07-30", "participants": [{"id": "User_A", "tokens": 100}]}
        realtime_leaderboard = [{"id": "User_A", "display_name": "甲", "tokens": 50}]

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(archive_snapshot, None)), \
             mock.patch.object(community, "get_community_stats", return_value={"leaderboard": realtime_leaderboard}):
            result = community.get_community_history(period="week", today=datetime.date(2026, 7, 30))

        # 今天已有归档 07-30，dates 里 07-30 只出现一次
        self.assertEqual(result["dates"].count("2026-07-30"), 1)

    def test_history_does_not_append_today_when_outside_period(self):
        """today 不落在所选自然周期内时，不补全实时数据（避免越界）。"""
        # today=08-15，但 period=week 范围是 07-27~08-02，08-15 不在范围内
        files = [{"name": "2026-07-30.json", "download_url": "https://example.test/30"}]
        archive_snapshot = {"date": "2026-07-30", "participants": [{"id": "User_A", "tokens": 100}]}
        realtime_leaderboard = [{"id": "User_B", "display_name": "乙", "tokens": 999}]

        with mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(archive_snapshot, None)), \
             mock.patch.object(community, "get_community_stats", return_value={"leaderboard": realtime_leaderboard}):
            result = community.get_community_history(period="week", today=datetime.date(2026, 8, 15))

        # today=08-15 不在本周范围，dates 里不应出现 08-15
        self.assertNotIn("2026-08-15", result["dates"])
        # 8-15 的 User_B（实时榜第一名）不应出现在 series 里（因为没补全）
        series_ids = [s["id"] for s in result["series"]]
        self.assertNotIn("User_B", series_ids)


if __name__ == "__main__":
    unittest.main()
