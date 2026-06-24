# -*- mode: python ; coding: utf-8 -*-
# PyInstaller spec for Token Monitor Windows build
# 用法: pyinstaller token_monitor.spec --noconfirm

import os

block_cipher = None

a = Analysis(
    ['start_windows.py'],
    pathex=[os.path.abspath('.')],
    binaries=[],
    datas=[
        ('server.py', '.'),
        ('scanner.py', '.'),
        ('index.html', '.'),
        ('chart.js', '.'),
    ],
    hiddenimports=[
        # scanner.py 用到的
        'sqlite3',
        'json',
        'urllib.request',
        're',
        'datetime',
        # server.py 用到的
        'http.server',
        'socketserver',
        'argparse',
        'plistlib',
    ],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=['tkinter', 'unittest', 'test', 'pydoc'],
    win_no_prefer_redirects=False,
    win_private_assemblies=False,
    cipher=block_cipher,
    noarchive=False,
)

pyz = PYZ(a.pure, a.zipped_data, cipher=block_cipher)

exe = EXE(
    pyz,
    a.scripts,
    a.binaries,
    a.zipfiles,
    a.datas,
    [],
    name='TokenMonitor',
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    upx_exclude=[],
    runtime_tmpdir=None,
    console=False,  # 无控制台窗口
    disable_windowed_traceback=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
    icon=None,  # 如有 .ico 可放 'AppIcon.ico'
)
