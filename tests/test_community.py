import base64
import datetime
import json
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
        with open(self.user_id_file, "w") as f:
            f.write("User_TEST")
        with open(self.optin_file, "w") as f:
            f.write("true")

        for name, value in (
            ("COMMUNITY_DIR", self.community_dir),
            ("USER_ID_FILE", self.user_id_file),
            ("OPTIN_FILE", self.optin_file),
        ):
            patcher = mock.patch.object(community, name, value)
            patcher.start()
            self.addCleanup(patcher.stop)

        community._aggregate_cache["data"] = None
        community._aggregate_cache["ts"] = 0

    def test_new_report_omits_empty_sha(self):
        write_calls = []

        def fake_api(method, path, data=None, token=None, require_auth=True):
            if method == "GET":
                return {"error": 404, "body": "not found"}
            write_calls.append((method, data))
            return {"content": {"path": path}}

        usage = {
            "summary": {"date": "2026-07-10", "total_tokens": 26_391_088},
            "by_tool": {"Codex": {"total_tokens": 20_000_000}},
        }
        with mock.patch.object(community, "_get_gitcode_token", return_value="test-token"), \
             mock.patch.object(community, "_gitcode_api", side_effect=fake_api):
            result = community.report_community_stats(usage)

        self.assertTrue(result["ok"])
        self.assertEqual(result["status"], "synced")
        self.assertEqual(len(write_calls), 1)
        self.assertEqual(write_calls[0][0], "POST")
        payload = write_calls[0][1]
        self.assertNotIn("sha", payload)
        self.assertEqual(payload["branch"], "community-data")

        report = json.loads(base64.b64decode(payload["content"]))
        self.assertEqual(report["today_tokens"], 26_391_088)
        self.assertEqual(report["report_date"], "2026-07-10")

    def test_existing_report_uses_put_with_sha(self):
        write_calls = []

        def fake_api(method, path, data=None, token=None, require_auth=True):
            if method == "GET":
                return {"sha": "existing-sha"}
            write_calls.append((method, data))
            return {"content": {"path": path}}

        usage = {
            "summary": {"date": "2026-07-10", "total_tokens": 123},
            "by_tool": {},
        }
        with mock.patch.object(community, "_get_gitcode_token", return_value="test-token"), \
             mock.patch.object(community, "_gitcode_api", side_effect=fake_api):
            result = community.report_community_stats(usage)

        self.assertTrue(result["ok"])
        self.assertEqual(write_calls[0][0], "PUT")
        self.assertEqual(write_calls[0][1]["sha"], "existing-sha")

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
            "id": "User_TEST",
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

        with mock.patch.object(community, "_get_gitcode_token", return_value="test-token"), \
             mock.patch.object(community, "_gitcode_api", side_effect=fake_api), \
             mock.patch.object(community, "_read_remote_json", side_effect=fake_read):
            result = community.get_community_stats()

        self.assertEqual(result["my_rank"], 12)
        self.assertEqual(result["rank_status"], "outside_top10")
        self.assertEqual(result["rank_total"], 12)
        self.assertEqual(len(result["leaderboard"]), 10)

    def test_public_data_remains_readable_without_upload_credentials(self):
        calls = []

        def fake_api(method, path, data=None, token=None, require_auth=True):
            calls.append((method, path, token, require_auth))
            return []

        with mock.patch.object(community, "_get_gitcode_token", return_value=None), \
             mock.patch.object(community, "_gitcode_api", side_effect=fake_api):
            result = community.get_community_stats()

        self.assertEqual(calls, [("GET", community.REPORTS_PATH, None, False)])
        self.assertNotIn("error", result)
        self.assertFalse(result["can_report"])
        self.assertEqual(result["rank_status"], "credential_missing")

    def test_report_read_failures_are_not_rendered_as_zero(self):
        files = [{"name": "User_BROKEN.json", "download_url": "https://example.test/broken.json"}]

        with mock.patch.object(community, "_get_gitcode_token", return_value=None), \
             mock.patch.object(community, "_gitcode_api", return_value=files), \
             mock.patch.object(community, "_read_remote_json", return_value=(None, "HTTP 502")):
            result = community.get_community_stats()

        self.assertEqual(result["data_status"], "load_failed")
        self.assertIn("全部读取失败", result["error"])


if __name__ == "__main__":
    unittest.main()
