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
        with open(self.user_id_file, "w") as f:
            f.write("User_TEST1")
        with open(self.optin_file, "w") as f:
            f.write("true")

        for name, value in (
            ("COMMUNITY_DIR", self.community_dir),
            ("USER_ID_FILE", self.user_id_file),
            ("OPTIN_FILE", self.optin_file),
            ("CREDENTIAL_FILE", self.credential_file),
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

    def test_history_cache_avoids_reloading_archives_within_ttl(self):
        files = [{"name": "2026-07-28.json", "download_url": "https://example.test/28"}]
        snapshot = {"date": "2026-07-28", "participants": [{"id": "User_28", "tokens": 28}]}

        with mock.patch.object(community, "_gitcode_api", return_value=files) as list_call, \
             mock.patch.object(community, "_read_remote_json", return_value=(snapshot, None)) as read_call:
            first = community.get_community_history(30)
            second = community.get_community_history(30)

        self.assertEqual(first["dates"], second["dates"])
        self.assertEqual(list_call.call_count, 1)
        self.assertEqual(read_call.call_count, 1)


if __name__ == "__main__":
    unittest.main()
