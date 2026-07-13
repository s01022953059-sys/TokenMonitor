import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import urlopen


ROOT = pathlib.Path(__file__).resolve().parents[1]


def unused_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class _RelayRecorder(BaseHTTPRequestHandler):
    calls = 0

    def do_POST(self):
        type(self).calls += 1
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"ok": True, "status": "synced"}).encode())

    def log_message(self, *_args):
        pass


class ServerCommunityIsolationTests(unittest.TestCase):
    def test_isolated_server_never_reports_to_real_or_test_relay(self):
        _RelayRecorder.calls = 0
        relay = ThreadingHTTPServer(("127.0.0.1", 0), _RelayRecorder)
        threading.Thread(target=relay.serve_forever, daemon=True).start()
        process = None
        try:
            with tempfile.TemporaryDirectory() as root:
                port = unused_port()
                env = os.environ.copy()
                env.update({
                    "HOME": os.path.join(root, "home"),
                    "TOKEN_MONITOR_LOCK_FILE": os.path.join(root, "server.lock"),
                    "TOKEN_MONITOR_HEATMAP_CACHE_FILE": os.path.join(root, "heatmap.json"),
                    "TOKEN_MONITOR_COMMUNITY_RELAY_URL": f"http://127.0.0.1:{relay.server_port}/v1/report",
                    "TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT": "1",
                })
                os.makedirs(env["HOME"], exist_ok=True)
                process = subprocess.Popen(
                    [sys.executable, "server.py", "--port", str(port)],
                    cwd=ROOT,
                    env=env,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    try:
                        with urlopen(f"http://127.0.0.1:{port}/api/app-info", timeout=1):
                            break
                    except OSError:
                        time.sleep(0.1)
                else:
                    self.fail("isolated server did not start")

                # The production loop waits five seconds before its first automatic report.
                time.sleep(5.5)
                self.assertEqual(_RelayRecorder.calls, 0)
        finally:
            if process and process.poll() is None:
                process.terminate()
                process.wait(timeout=5)
            relay.shutdown()
            relay.server_close()


if __name__ == "__main__":
    unittest.main()
