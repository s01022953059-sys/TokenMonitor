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


class UpdaterDownloadFailureFallbackTest(unittest.TestCase):
    """v1.5.20: 下载失败人工兜底契约。

    v1.5.18/1.5.19 事故里旧客户端下载 404 后只有误导性文案 ("稍后再试"),
    没有任何人工出口, 用户被卡死在坏版本。契约: 下载阶段失败必须
    (1) 自动用系统浏览器打开无查询串直链, (2) 前端失败态有手动下载链接,
    (3) 确定性 404/403/410 不消耗重试次数, (4) 不再出现 stale 文案。
    """

    def setUp(self):
        self.source = SWIFT.read_text(encoding="utf-8")
        self.frontends = {
            "macOS": (ROOT / "index.html").read_text(encoding="utf-8"),
            "Windows": (ROOT / "go_build" / "static" / "index.html").read_text(
                encoding="utf-8"
            ),
        }
        self.windows_gui = (ROOT / "go_build" / "gui_windows.go").read_text(encoding="utf-8")

    def test_browser_fallback_opens_original_url(self):
        # 兜底必须打开 feed 原始 URL (无查询串), 且经由 failAutoUpdate 统一入口
        self.assertIn("openBrowserFallback: Bool = false", self.source)
        self.assertIn("NSWorkspace.shared.open(update.downloadURL)", self.source)

    def test_browser_fallback_runs_on_main_thread(self):
        # NSWorkspace.open 必须在 DispatchQueue.main.async 块内 (6d7dd889 教训)
        idx = self.source.find("private func failAutoUpdate")
        self.assertGreater(idx, 0)
        block = self.source[idx:idx + 1200]
        self.assertLess(block.find("DispatchQueue.main.async"),
                        block.find("NSWorkspace.shared.open"))

    def test_download_failures_route_to_browser_fallback(self):
        # 首次下载网络错误 / HTTP 失败、重试耗尽、重试网络错误都要带兜底
        self.assertGreaterEqual(self.source.count("openBrowserFallback: true"), 5)

    def test_deterministic_http_failures_not_retried(self):
        # 404/403/410 是确定性失败, 直接终局, 不消耗重试次数
        self.assertIn("code == 404 || code == 403 || code == 410", self.source)

    def test_stale_and_misleading_copy_removed(self):
        self.assertNotIn("下载 zip", self.source)
        self.assertNotIn("502/504, 稍后再试", self.source)

    def test_external_url_bridge_is_allowlisted(self):
        # 前端桥只放行 https + gitcode.com, 防任意 URL 注入
        self.assertIn('case "openExternalURL":', self.source)
        self.assertIn('u.scheme == "https"', self.source)
        self.assertIn('host == "gitcode.com" || host.hasSuffix(".gitcode.com")', self.source)
        self.assertIn('w.Bind("openExternalURL"', self.windows_gui)
        self.assertIn('u.Scheme != "https"', self.windows_gui)

    def test_frontend_renders_manual_download_link(self):
        for name, html in self.frontends.items():
            with self.subTest(frontend=name):
                self.assertIn('id="aboutManualDownloadLink"', html)
                # 失败态链接直链来自 Swift 第三参或 /api/check-update
                self.assertIn("window.__tmSetUpdateStatus = function(text, kind, downloadURL)", html)
                self.assertIn("aboutManualDownloadUrl", html)
                self.assertIn("action: 'openExternalURL'", html)

    def test_frontends_stay_in_sync(self):
        self.assertEqual(self.frontends["macOS"], self.frontends["Windows"])


class DmgMountNoPipeDeadlockTest(unittest.TestCase):
    """v1.5.21: DMG 挂载不得用 Pipe + readDataToEndOfFile (永久死锁事故)。

    v1.5.15~v1.5.20 的 installFromDMG 用 Pipe 读 hdiutil -plist 输出;
    hdiutil fork 的 diskimages-helper 继承 pipe 写端并常驻维持挂载,
    EOF 永不到来, 下载线程死锁在"挂载安装镜像" (2026-09-06 预演实证)。
    契约: stdout 重定向到暂存文件 + 先 waitUntilExit 再读文件。
    """

    def setUp(self):
        self.source = SWIFT.read_text(encoding="utf-8")

    def test_no_pipe_read_for_hdiutil_attach(self):
        self.assertNotIn("attachPipe.fileHandleForReading.readDataToEndOfFile", self.source)
        self.assertNotIn("let attachPipe = Pipe()", self.source)

    def test_mount_plist_parser_supports_both_formats(self):
        # macOS 26/27 的 hdiutil -plist 顶层是 {"system-entities": [...]},
        # 旧 macOS 是顶层数组; 解析器必须两者都认 (v1.5.21 修复)
        self.assertIn('"system-entities"', self.source)
        self.assertIn("mountPoint(fromAttachPlist", self.source)

    def test_attach_stdout_goes_to_file_and_wait_first(self):
        # stdout 必须是可写 FileHandle (macOS 27 传 URL 给 standardOutput 会 trap)
        self.assertIn("attach.standardOutput = attachOutHandle", self.source)
        self.assertIn("FileHandle(forWritingTo: attachOutURL)", self.source)
        idx_wait = self.source.find("attach.waitUntilExit()")
        idx_read = self.source.find("Data(contentsOf: attachOutURL)")
        self.assertGreater(idx_wait, 0)
        self.assertGreater(idx_read, idx_wait, "必须先 waitUntilExit 再读暂存文件")


if __name__ == "__main__":
    unittest.main()
