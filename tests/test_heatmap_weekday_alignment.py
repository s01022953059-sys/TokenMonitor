"""热力图左侧星期标签对齐回归测试。

背景: .heatmap-weekday-labels width 太窄 + 缺少 white-space: nowrap,
中文 '一/二/三/四/五/六/日' 在 9px 字号下会折行, 显示成两行
(用户报告左侧 '一二三四五六日' 七个字都断行)。

本测试确保双端 index.html 都有正确的 CSS 契约:
- .heatmap-weekday-labels 容器宽度足够容纳最宽汉字 + 不应被强制折行
- 每个 span 自身 nowrap 且左对齐
- 7 个 span 与 7 个日期列 (heatmap-week 内 7 行) 对齐
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def _extract_heatmap_css(html: str) -> str:
    """取所有 .heatmap-weekday-labels { ... } 和 .heatmap-weekday-labels span { ... }
    定义块的合并文本 (暗色 + 亮色两处)。"""
    blocks = re.findall(r"\.heatmap-weekday-labels(?:\s+span)?\s*\{[^}]*\}", html)
    return "\n".join(blocks)


def _extract_weekday_label_html(html: str) -> str:
    """取构造 .heatmap-weekday-labels 那段 JS 拼接出来的模板 (7 个 span)。

    注意: JS 里循环写 '<span>' + ... + '</span>' 共 7 次, 用贪心匹配避免
    在第一个 '</div>' 处停下 (因为注释或后续 gridHtml 拼接里有 </div>)."""
    m = re.search(
        r"gridHtml \+= '<div class=\"heatmap-weekday-labels\">(.*?)gridHtml \+= '</div>'",
        html,
        re.DOTALL,
    )
    return m.group(1) if m else ""


def _read(html_path: Path) -> str:
    return html_path.read_text(encoding="utf-8")


class HeatmapWeekdayAlignmentTests(unittest.TestCase):
    def test_frontends_stay_in_sync(self):
        """双端 index.html 必须等价, 否则 macOS / Windows 客户端周日历行高不同。"""
        mac = _read(ROOT / "index.html")
        windows = _read(ROOT / "go_build" / "static" / "index.html")
        self.assertEqual(mac, windows)

    def test_weekday_label_container_has_white_space_nowrap(self):
        """修复前 width: 20px + flex-direction: column + 缺 nowrap, 中文字符
        在 9px 字号下会断行。修复后: 容器宽度放够 (>= 22px) 且 span 加 nowrap。"""
        for label, html_path in [
            ("mac", ROOT / "index.html"),
            ("windows", ROOT / "go_build" / "static" / "index.html"),
        ]:
            with self.subTest(client=label):
                html = _read(html_path)
                css = _extract_heatmap_css(html)
                # 容器宽度: 必须够容 1 个 9px 汉字 (>= 20px 不够, 实测要 >= 22px)
                m = re.search(r"\.heatmap-weekday-labels\s*\{[^}]*width:\s*(\d+)px", css)
                self.assertIsNotNone(m, f"{label}: 找不到 width 声明")
                width = int(m.group(1))
                self.assertGreaterEqual(
                    width, 22,
                    f"{label}: width={width}px, 太窄, 中文 9px 汉字会断行 (>= 22px 才够)",
                )
                # span 必须 white-space: nowrap (防止单字也断行)。
                # CSS 中容器 .heatmap-weekday-labels { ... } 与子元素 .heatmap-weekday-labels span { ... }
                # 是连续两个 block, regex 需用 DOTALL 才能跨行匹配。
                self.assertRegex(
                    css,
                    r"\.heatmap-weekday-labels\s+span\s*\{[\s\S]*?white-space:\s*nowrap",
                    f"{label}: span 缺 white-space: nowrap, 单字仍可能折行",
                )

    def test_weekday_label_marks_text_align_left(self):
        """容器或子元素必须有左对齐, 否则 '一' / '三' / '日' 不同宽字符会
        水平错位 (视觉效果丑)。"""
        for label, html_path in [
            ("mac", ROOT / "index.html"),
            ("windows", ROOT / "go_build" / "static" / "index.html"),
        ]:
            with self.subTest(client=label):
                html = _read(html_path)
                css = _extract_heatmap_css(html)
                # 容器: text-align: left OR align-items: flex-start (flex column 时)
                container_left = (
                    "text-align: left" in css
                    or "align-items: flex-start" in css
                )
                self.assertTrue(
                    container_left,
                    f"{label}: 容器需要 text-align: left 或 align-items: flex-start 让左边缘对齐",
                )

    def test_weekday_label_html_template_has_seven_spans(self):
        """HTML 模板必须生成 7 个 span, 与 7 行周列对齐 (每个 span 对应一周一行)。

        JS 用 'for (let i = 0; i < 7; i++)' 循环拼接 '<span>' + ... + '</span>',
        实际产物是 7 个 span。本测试检查循环条件与 span 模板同时存在,
        防回归 (比如有人改成 'i < 6' 或忘了 span 模板)。"""
        for label, html_path in [
            ("mac", ROOT / "index.html"),
            ("windows", ROOT / "go_build" / "static" / "index.html"),
        ]:
            with self.subTest(client=label):
                html = _read(html_path)
                template = _extract_weekday_label_html(html)
                # 循环条件必须有 i < 7
                self.assertRegex(
                    template, r"i\s*<\s*7",
                    f"{label}: 循环条件需 i < 7, 实际模板里找不到",
                )
                # 模板里有 span 模板 ('<span>' 字面量)
                self.assertRegex(
                    template, r"'<span>'",
                    f"{label}: 模板缺少 '<span>' 字面量",
                )


if __name__ == "__main__":
    unittest.main()