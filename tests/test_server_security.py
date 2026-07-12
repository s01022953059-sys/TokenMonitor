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

    def test_authenticated_file_webview_origins_are_allowed(self):
        for origin in (
            "null",
            "file://",
            "file:///Users/test/Applications/Token%20Monitor.app/Contents/Resources/index.html",
            "applewebdata://local/index.html",
            "webkit-masked-url://local/index.html",
        ):
            with self.subTest(origin=origin):
                self.assertTrue(server._is_allowed_profile_origin(origin, "local-test-token"))

    def test_unauthenticated_file_webview_is_rejected(self):
        for origin in ("null", "file://", "file:///tmp/index.html", "applewebdata://local/index.html", "webkit-masked-url://local/index.html"):
            with self.subTest(origin=origin):
                self.assertFalse(server._is_allowed_profile_origin(origin, ""))
                self.assertFalse(server._is_allowed_profile_origin(origin, "wrong-token"))

    def test_loopback_origin_remains_allowed(self):
        self.assertTrue(server._is_allowed_profile_origin("http://127.0.0.1:15723", ""))
        self.assertTrue(server._is_allowed_profile_origin("http://localhost:15723", ""))

    def test_remote_origin_is_rejected_even_with_token(self):
        self.assertFalse(server._is_allowed_profile_origin("https://example.com", "local-test-token"))


if __name__ == "__main__":
    unittest.main()
