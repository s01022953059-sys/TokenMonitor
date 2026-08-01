"""验证 / 和 /index.html 响应携带 no-cache 头, 避免 WKWebView 在更新后
继续渲染旧版 HTML 导致指标全 0。

背景: v1.4.86 之前 Swift 用 loadFileURL 走 file:// 协议, server 的
Cache-Control 头根本到不了 WebKit。修法: Swift 改走 http 协议, server
端保证响应带 no-store/no-cache/must-revalidate 三件套。"""

import os
import threading
import time
import unittest

os.environ.setdefault("TOKEN_MONITOR_LOCK_FILE", "/tmp/token-monitor-cache-test.lock")

import server


def _start_test_server():
    """找一个空闲端口, 起 server, 返回 (port, shutdown_fn)。"""
    import socket
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()

    httpd = server.ThreadingHTTPServer(("127.0.0.1", port), server.TokenMonitorHandler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()

    def shutdown():
        httpd.shutdown()
        httpd.server_close()

    return port, shutdown


class StaticAssetCacheHeadersTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.port, cls.shutdown = _start_test_server()
        time.sleep(0.1)  # 等 server 进入 accept 循环

    @classmethod
    def tearDownClass(cls):
        cls.shutdown()

    def _get_headers(self, path):
        import urllib.request
        req = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}")
        with urllib.request.urlopen(req, timeout=2) as resp:
            return dict(resp.headers)

    def test_root_response_has_no_cache_control(self):
        headers = self._get_headers("/")
        cc = headers.get("Cache-Control", "")
        self.assertIn("no-store", cc)
        self.assertIn("no-cache", cc)
        self.assertIn("must-revalidate", cc)

    def test_index_html_response_has_no_cache_control(self):
        headers = self._get_headers("/index.html")
        cc = headers.get("Cache-Control", "")
        self.assertIn("no-store", cc)
        self.assertIn("no-cache", cc)
        self.assertIn("must-revalidate", cc)

    def test_index_html_response_has_pragma_no_cache(self):
        """IE/旧版 WebKit 看 Pragma, 兼容性兜底。"""
        headers = self._get_headers("/index.html")
        self.assertEqual(headers.get("Pragma"), "no-cache")


if __name__ == "__main__":
    unittest.main()