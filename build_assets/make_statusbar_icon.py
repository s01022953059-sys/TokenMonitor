#!/usr/bin/env python3
"""生成 NSStatusBar 菜单栏图标 (template image) 从 icon.png master。

macOS 状态栏图标必须是 alpha-only 模板图, 系统按主题深/浅自动着色。
需要的 Python 依赖: Pillow (pip3 install Pillow)。

用法: python3 build_assets/make_statusbar_icon.py
"""
from PIL import Image
import os

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(os.path.dirname(HERE), 'icon.png')

# macOS 菜单栏标准尺寸 (@1x + @2x 视网膜)
SIZES = [
    (18, 'icon_18.png'),
    (36, 'icon_18@2x.png'),
    (32, 'icon_32.png'),
    (64, 'icon_32@2x.png'),
]


def main():
    if not os.path.exists(SRC):
        raise SystemExit(f'master 图标缺失: {SRC}')

    img = Image.open(SRC).convert('RGBA')

    for size, name in SIZES:
        out = img.resize((size, size), Image.LANCZOS)
        # 提取 alpha, RGB 设为全 0, 让 macOS 按主题着色
        r, g, b, a = out.split()
        template = Image.merge('RGBA', (
            Image.new('L', (size, size), 0),
            Image.new('L', (size, size), 0),
            Image.new('L', (size, size), 0),
            a,
        ))
        out_path = os.path.join(HERE, name)
        template.save(out_path)
        print(f'  saved {name} ({size}x{size})')

    print('done')


if __name__ == '__main__':
    main()
