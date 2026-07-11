import unittest

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


if __name__ == "__main__":
    unittest.main()
