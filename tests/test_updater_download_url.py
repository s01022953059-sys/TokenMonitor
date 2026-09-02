"""macOS 更新器下载地址契约 (app_wrapper.swift)。

v1.3.25 起 Swift 更新器给下载 URL 追加 ?_tm= cache buster;
GitCode 下载端点对带任意查询串的 URL 一律返回 404 (已用 curl 实证:
无参数 206, 带 ?_tm= / ?x=1 均 404), v1.5.19 发布当日 macOS 自动
更新全量失败 (tm_debug.log: download HTTP 404)。

契约: 下载地址保持 feed 原样, 防缓存只靠 urlCache=nil +
reloadIgnoringLocalCacheData + Cache-Control/Pragma no-cache header。
"""

import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

SWIFT = ROOT / "app_wrapper.swift"


class UpdaterDownloadURLTest(unittest.TestCase):
    def setUp(self):
        self.source = SWIFT.read_text(encoding="utf-8")

    def test_no_cache_buster_query_is_appended(self):
        # 首次下载与重试下载都不允许再拼 ?_tm= 查询参数
        self.assertNotIn('URLQueryItem(name: "_tm"', self.source)

    def test_download_uses_feed_url_directly(self):
        # 两处下载都用 let downloadURL = update.downloadURL 直读 feed 地址
        self.assertEqual(2, self.source.count("let downloadURL = update.downloadURL"))
        self.assertNotIn("var downloadURL = update.downloadURL", self.source)

    def test_cache_avoidance_mechanisms_are_preserved(self):
        # 去掉 query buster 后, 防缓存职责全部落在配置与 header 上, 不可回退
        self.assertIn("config.urlCache = nil", self.source)
        self.assertIn("config.requestCachePolicy = .reloadIgnoringLocalCacheData", self.source)
        self.assertIn("request.cachePolicy = .reloadIgnoringLocalCacheData", self.source)
        self.assertIn(
            'request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")', self.source
        )
        self.assertIn(
            'request.setValue("no-cache", forHTTPHeaderField: "Pragma")', self.source
        )

    def test_incident_is_documented_for_future_maintainers(self):
        # 防止未来再把 cache buster 加回去: 事故注释必须留在下载处
        self.assertIn("GitCode 下载端点对带任意查询串的 URL 一律返回 404", self.source)


if __name__ == "__main__":
    unittest.main()
