import datetime
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

    def test_new_user_id_is_always_eight_characters(self):
        self.assertRegex(community._new_user_id(), r"^User_[A-Z0-9]{8}$")

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
        self.assertEqual(result["all_reporters"], 2)
        self.assertEqual(result["total_tokens_today"], 31_000_010)
        self.assertEqual([item["id"] for item in result["leaderboard"]], ["User_TEST1", "User_OTHER"])
        self.assertEqual(result["leaderboard"][0]["display_name"], "鹏帅")
        self.assertEqual(result["my_display_name"], "鹏帅")


if __name__ == "__main__":
    unittest.main()
