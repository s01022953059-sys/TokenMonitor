#!/usr/bin/env python3
"""Token Monitor Windows 启动器。

macOS 版用 start.sh (bash), Windows 版用这个 Python 脚本。
功能: 单实例检查 → 后台启动 server.py → 打开浏览器。

用法:
    python start_windows.py            # 启动
    python start_windows.py stop       # 停止
    python start_windows.py status     # 状态
"""
import os
import sys
import json
import subprocess
import tempfile
import time
import webbrowser

DIR = os.path.dirname(os.path.abspath(__file__))
PID_FILE = os.path.join(tempfile.gettempdir(), "token_monitor_server.pid")
PORT = 15723
UPDATE_FEED_URL = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest"


def _read_pid():
    if os.path.exists(PID_FILE):
        try:
            return int(open(PID_FILE).read().strip())
        except (ValueError, IOError):
            pass
    return None


def _is_running(pid):
    if not pid:
        return False
    try:
        # Windows: tasklist; Unix: ps
        if sys.platform == "win32":
            result = subprocess.run(
                ["tasklist", "/FI", f"PID eq {pid}"],
                capture_output=True, text=True
            )
            return str(pid) in result.stdout
        else:
            os.kill(pid, 0)
            return True
    except (OSError, subprocess.SubprocessError):
        return False


def cmd_start():
    pid = _read_pid()
    if pid and _is_running(pid):
        print(f"[!] Token Monitor 已在运行 (PID: {pid})")
        webbrowser.open(f"http://127.0.0.1:{PORT}")
        return

    print("[*] 正在启动 Token Monitor...")

    # 启动 server.py (后台)
    if sys.platform == "win32":
        # Windows: 用 CREATE_NO_WINDOW 隐藏控制台
        proc = subprocess.Popen(
            [sys.executable, os.path.join(DIR, "server.py"),
             "--port", str(PORT),
             "--update-feed-url", UPDATE_FEED_URL],
            creationflags=subprocess.CREATE_NO_WINDOW if hasattr(subprocess, "CREATE_NO_WINDOW") else 0,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    else:
        proc = subprocess.Popen(
            [sys.executable, os.path.join(DIR, "server.py"),
             "--port", str(PORT),
             "--update-feed-url", UPDATE_FEED_URL],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

    with open(PID_FILE, "w") as f:
        f.write(str(proc.pid))

    # 等待服务就绪
    import urllib.request
    for _ in range(15):
        time.sleep(0.5)
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{PORT}/api/app-info", timeout=1)
            print(f"[+] 启动成功! (PID: {proc.pid})")
            webbrowser.open(f"http://127.0.0.1:{PORT}")
            return
        except Exception:
            pass

    print(f"[+] 服务已启动 (PID: {proc.pid}), 正在打开浏览器...")
    webbrowser.open(f"http://127.0.0.1:{PORT}")


def cmd_stop():
    pid = _read_pid()
    if pid and _is_running(pid):
        print(f"[*] 正在停止 (PID: {pid})...")
        try:
            if sys.platform == "win32":
                subprocess.run(["taskkill", "/PID", str(pid), "/F"],
                               capture_output=True)
            else:
                os.kill(pid, 15)
        except OSError:
            pass
        print("[+] 已停止")
    else:
        print("[-] 未检测到运行中的实例")
    if os.path.exists(PID_FILE):
        os.remove(PID_FILE)


def cmd_status():
    pid = _read_pid()
    if pid and _is_running(pid):
        print(f"[●] 运行中 (PID: {pid}) → http://127.0.0.1:{PORT}")
    else:
        print("[○] 已停止")


if __name__ == "__main__":
    action = sys.argv[1] if len(sys.argv) > 1 else "start"
    if action == "start":
        cmd_start()
    elif action == "stop":
        cmd_stop()
    elif action == "status":
        cmd_status()
    else:
        print(f"用法: python {sys.argv[0]} [start|stop|status]")
        sys.exit(1)
