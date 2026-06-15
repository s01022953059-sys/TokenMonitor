#!/usr/bin/env python3
"""
Token Monitor — Windows 启动器
双击即可启动本地服务并自动打开浏览器大屏。
支持系统托盘驻留，关闭浏览器后服务不会退出。
"""
import os
import sys
import time
import socket
import threading
import webbrowser
import subprocess

PORT = 15723
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

def is_port_in_use(port):
    """检测端口是否已被占用"""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(0.3)
        return s.connect_ex(('127.0.0.1', port)) == 0

def start_server():
    """在后台线程启动 Python HTTP 服务"""
    server_script = os.path.join(SCRIPT_DIR, 'server.py')
    # 使用 pythonw（如有）避免弹出控制台窗口
    python_exe = sys.executable
    if python_exe.endswith('python.exe'):
        pythonw = python_exe.replace('python.exe', 'pythonw.exe')
        if os.path.exists(pythonw):
            python_exe = pythonw
    
    subprocess.Popen(
        [python_exe, server_script],
        cwd=SCRIPT_DIR,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0)
    )

def wait_for_server(timeout=10):
    """等待服务器就绪"""
    start = time.time()
    while time.time() - start < timeout:
        if is_port_in_use(PORT):
            return True
        time.sleep(0.1)
    return False

def try_tray_icon():
    """尝试创建系统托盘图标（需要 pystray + Pillow）"""
    try:
        import pystray
        from PIL import Image, ImageDraw
        
        # 绘制一个简单的火焰图标
        img = Image.new('RGBA', (64, 64), (0, 0, 0, 0))
        draw = ImageDraw.Draw(img)
        # 橙色火焰外形
        draw.ellipse([12, 8, 52, 56], fill=(255, 120, 30, 230))
        draw.ellipse([18, 16, 46, 52], fill=(255, 180, 50, 230))
        draw.ellipse([24, 28, 40, 52], fill=(255, 230, 100, 230))
        
        def open_browser(icon, item):
            webbrowser.open(f'http://127.0.0.1:{PORT}')
        
        def quit_app(icon, item):
            icon.stop()
            os._exit(0)
        
        menu = pystray.Menu(
            pystray.MenuItem('打开大屏', open_browser, default=True),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem('退出', quit_app)
        )
        
        icon = pystray.Icon('TokenMonitor', img, 'Token Monitor', menu)
        icon.run()
    except ImportError:
        # pystray 未安装，用简单的 input 阻塞代替
        print("\n🔥 Token Monitor 已启动")
        print(f"📊 大屏地址: http://127.0.0.1:{PORT}")
        print("💡 提示: pip install pystray pillow 可启用系统托盘图标")
        print("\n按 Ctrl+C 退出...\n")
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            print("\n正在退出...")

def main():
    # 1. 如果服务已在运行，直接打开浏览器
    if is_port_in_use(PORT):
        webbrowser.open(f'http://127.0.0.1:{PORT}')
        try_tray_icon()
        return
    
    # 2. 启动后台服务
    start_server()
    
    # 3. 等待服务就绪
    if wait_for_server():
        webbrowser.open(f'http://127.0.0.1:{PORT}')
    else:
        # 如果超时则也打开，让浏览器自己重试
        webbrowser.open(f'http://127.0.0.1:{PORT}')
    
    # 4. 进入托盘模式或保持运行
    try_tray_icon()

if __name__ == '__main__':
    main()
