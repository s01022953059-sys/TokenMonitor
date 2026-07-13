import unittest
from unittest import mock

import server


class UpdateFeedTests(unittest.TestCase):
    def test_release_asset_is_preferred_over_release_page(self):
        payload = {
            "tag_name": "v9.9.9",
            "html_url": "https://example.test/releases/v9.9.9",
            "assets": [
                {
                    "name": "Token Monitor.dmg",
                    "browser_download_url": "https://example.test/Token-Monitor.dmg",
                }
            ],
        }

        info = server._extract_release_info(payload)

        self.assertEqual(info["download_url"], "https://example.test/Token-Monitor.dmg")

    def test_release_page_is_only_the_last_fallback(self):
        payload = {
            "tag_name": "v9.9.9",
            "html_url": "https://example.test/releases/v9.9.9",
            "assets": [],
        }

        info = server._extract_release_info(payload)

        self.assertEqual(info["download_url"], payload["html_url"])

    def test_gitcode_api_attachment_url_is_normalized(self):
        payload = {
            "tag_name": "v9.9.9",
            "assets": [{
                "name": "Token Monitor.dmg",
                "browser_download_url": "https://api.gitcode.com/acme/app/releases/download/v9.9.9/Token Monitor.dmg",
            }],
        }

        info = server._extract_release_info(payload)

        self.assertEqual(
            info["download_url"],
            "https://gitcode.com/acme/app/releases/download/v9.9.9/Token Monitor.dmg",
        )

    def test_update_request_respects_system_proxy_settings(self):
        request = server.urlrequest.Request("https://updates.example.test/latest")
        response = mock.MagicMock()
        opener = mock.MagicMock()
        opener.open.return_value = response
        with mock.patch.object(server.urlrequest, "getproxies", return_value={"https": "http://127.0.0.1:7890"}), mock.patch.object(
            server.urlrequest, "build_opener", return_value=opener
        ) as build_opener:
            self.assertIs(server._open_external_request(request, 8), response)

        proxy_handler = build_opener.call_args.args[0]
        self.assertEqual(proxy_handler.proxies["https"], "http://127.0.0.1:7890")
        opener.open.assert_called_once_with(request, timeout=8)


if __name__ == "__main__":
    unittest.main()
