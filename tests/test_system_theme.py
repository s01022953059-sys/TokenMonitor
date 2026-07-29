import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class SystemThemeTest(unittest.TestCase):
    def test_frontends_follow_system_theme_with_temporary_debug_toggle(self):
        mac_html = (ROOT / "index.html").read_text(encoding="utf-8")
        windows_html = (ROOT / "go_build" / "static" / "index.html").read_text(
            encoding="utf-8"
        )
        self.assertEqual(mac_html, windows_html)

        self.assertIn("window.matchMedia('(prefers-color-scheme: light)')", mac_html)
        self.assertIn("function getSystemTheme()", mac_html)
        self.assertIn("function applySystemTheme()", mac_html)
        self.assertIn("systemThemeQuery.addEventListener('change', applySystemTheme)", mac_html)
        self.assertIn("localStorage.removeItem('token-monitor-theme')", mac_html)
        self.assertIn('id="themeToggleBtn"', mac_html)
        self.assertIn("function toggleThemePreview()", mac_html)
        self.assertIn("addEventListener('click', toggleThemePreview)", mac_html)
        self.assertIn("重启后恢复跟随系统", mac_html)

        self.assertNotIn("localStorage.setItem('token-monitor-theme'", mac_html)
        self.assertNotIn("localStorage.getItem('token-monitor-theme'", mac_html)


if __name__ == "__main__":
    unittest.main()
