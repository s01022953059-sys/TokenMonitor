import os
import unittest

os.environ.setdefault("TOKEN_MONITOR_LOCK_FILE", "/tmp/token-monitor-server-security-test.lock")

import server


class ProfileOriginSecurityTests(unittest.TestCase):
    def setUp(self):
        self.original_token = server.LOCAL_API_TOKEN
        server.LOCAL_API_TOKEN = "local-test-token"

    def tearDown(self):
        server.LOCAL_API_TOKEN = self.original_token

    def test_authenticated_file_webview_is_allowed(self):
        self.assertTrue(server._is_allowed_profile_origin("null", "local-test-token"))

    def test_unauthenticated_file_webview_is_rejected(self):
        self.assertFalse(server._is_allowed_profile_origin("null", ""))
        self.assertFalse(server._is_allowed_profile_origin("null", "wrong-token"))

    def test_loopback_origin_remains_allowed(self):
        self.assertTrue(server._is_allowed_profile_origin("http://127.0.0.1:15723", ""))
        self.assertTrue(server._is_allowed_profile_origin("http://localhost:15723", ""))

    def test_remote_origin_is_rejected_even_with_token(self):
        self.assertFalse(server._is_allowed_profile_origin("https://example.com", "local-test-token"))


if __name__ == "__main__":
    unittest.main()
