"""_read_app_version 候选顺序回归测试。

2026-08-28 事故: server.py 的第一个候选 Resources/Info.plist 在真实 bundle
里不存在 (真实 plist 在 Contents/Info.plist), 导致从 ~/Applications 启动的
旧版本 (1.5.13) 误读 /Applications 新副本 (1.5.16) 的版本号——About 显示
"已是最新 v1.5.16", 而 Swift 更新器按自己 bundle 的 1.5.13 不断点亮红点。
"""

import os
import plistlib
import tempfile
import unittest
from unittest import mock

import server


def _write_plist(path, version):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        plistlib.dump({"CFBundleShortVersionString": version}, f)


def _sandbox_exists(tmp):
    """屏蔽测试机上真实安装的 Token Monitor.app, 只放行 tmp 沙箱内的路径。"""
    real_exists = os.path.exists

    def fake_exists(path):
        path = str(path)
        if "Token Monitor.app" in path and not path.startswith(str(tmp)):
            return False
        return real_exists(path)

    return fake_exists


class ReadAppVersionTests(unittest.TestCase):
    def test_own_bundle_plist_wins_over_other_copies(self):
        """必须优先读自己 bundle 的 Contents/Info.plist, 而不是其他副本的。"""
        with tempfile.TemporaryDirectory() as tmp:
            bundle_contents = os.path.join(tmp, "Token Monitor.app", "Contents")
            _write_plist(os.path.join(bundle_contents, "Info.plist"), "9.9.9")
            other_apps = os.path.join(tmp, "other-location")
            _write_plist(
                os.path.join(
                    other_apps, "Token Monitor.app", "Contents", "Info.plist"
                ),
                "8.8.8",
            )

            with mock.patch.object(
                server,
                "__file__",
                os.path.join(bundle_contents, "Resources", "server.py"),
            ), mock.patch.object(
                server.os.path,
                "expanduser",
                side_effect=lambda p: p.replace("~", other_apps, 1),
            ), mock.patch.object(
                server.os.path, "exists", side_effect=_sandbox_exists(tmp)
            ):
                self.assertEqual(server._read_app_version(), "9.9.9")

    def test_flat_dev_layout_still_reads_adjacent_plist(self):
        """开发态平铺布局 (server.py 与 Info.plist 同目录) 仍然可读。"""
        with tempfile.TemporaryDirectory() as tmp:
            _write_plist(os.path.join(tmp, "Info.plist"), "7.7.7")

            with mock.patch.object(
                server, "__file__", os.path.join(tmp, "server.py")
            ), mock.patch.object(
                server.os.path,
                "expanduser",
                side_effect=lambda p: os.path.join(tmp, "home", p[1:])
                if p.startswith("~")
                else p,
            ), mock.patch.object(
                server.os.path, "exists", side_effect=_sandbox_exists(tmp)
            ):
                self.assertEqual(server._read_app_version(), "7.7.7")

    def test_returns_placeholder_when_nothing_readable(self):
        """所有候选都不可读时返回占位版本, 不抛异常。"""
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.object(
                server, "__file__", os.path.join(tmp, "server.py")
            ), mock.patch.object(
                server.os.path,
                "expanduser",
                side_effect=lambda p: os.path.join(tmp, "home", p[1:])
                if p.startswith("~")
                else p,
            ), mock.patch.object(
                server.os.path, "exists", side_effect=_sandbox_exists(tmp)
            ):
                self.assertEqual(server._read_app_version(), "0.0-dev")


if __name__ == "__main__":
    unittest.main()
