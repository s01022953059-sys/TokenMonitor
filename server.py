#!/usr/bin/env python3
import http.server
import socketserver
import json
import os
import sys

# 引入本地的 scanner 模块
sys.path.append(os.path.dirname(os.path.abspath(__file__)))
try:
    from scanner import get_today_usage, get_historical_usage
except ImportError:
    # 兼容相对路径执行
    from .scanner import get_today_usage, get_historical_usage

PORT = 15723
DIRECTORY = os.path.dirname(os.path.abspath(__file__))
APP_VERSION = "1.1"
UPDATE_FEED_URL = "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest"

class TokenMonitorHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        # 托管当前文件夹，确保前端 html 页面和 Chart.js 被正确分发
        super().__init__(*args, directory=DIRECTORY, **kwargs)

    def end_headers(self):
        # 开启跨域及缓存机制
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'GET, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type')
        # 确保静态页面和数据接口均不被客户端缓存，保证更新能即时呈现
        self.send_header('Cache-Control', 'no-store, no-cache, must-revalidate, max-age=0')
        self.send_header('Pragma', 'no-cache')
        self.send_header('Expires', '0')
        super().end_headers()

    def do_OPTIONS(self):
        self.send_response(200)
        self.end_headers()

    def do_GET(self):
        if self.path == '/api/usage':
            try:
                data = get_today_usage()
                response_body = json.dumps(data).encode('utf-8')
                
                self.send_response(200)
                self.send_header('Content-Type', 'application/json; charset=utf-8')
                self.send_header('Content-Length', str(len(response_body)))
                self.end_headers()
                self.wfile.write(response_body)
            except Exception as e:
                self.send_response(500)
                self.send_header('Content-Type', 'application/json; charset=utf-8')
                self.end_headers()
                err_resp = json.dumps({"error": str(e)}).encode('utf-8')
                self.wfile.write(err_resp)
        elif self.path == '/api/history':
            try:
                data = get_historical_usage()
                response_body = json.dumps(data).encode('utf-8')
                
                self.send_response(200)
                self.send_header('Content-Type', 'application/json; charset=utf-8')
                self.send_header('Content-Length', str(len(response_body)))
                self.end_headers()
                self.wfile.write(response_body)
            except Exception as e:
                self.send_response(500)
                self.send_header('Content-Type', 'application/json; charset=utf-8')
                self.end_headers()
                err_resp = json.dumps({"error": str(e)}).encode('utf-8')
                self.wfile.write(err_resp)
        elif self.path == '/api/app-info':
            data = {
                "name": "Token Monitor",
                "version": APP_VERSION,
                "update_feed_url": UPDATE_FEED_URL,
                "update_enabled": bool(UPDATE_FEED_URL),
            }
            response_body = json.dumps(data).encode('utf-8')

            self.send_response(200)
            self.send_header('Content-Type', 'application/json; charset=utf-8')
            self.send_header('Content-Length', str(len(response_body)))
            self.end_headers()
            self.wfile.write(response_body)
        else:
            # 路由重定向，输入根路径默认分发 index.html
            if self.path == '/' or self.path == '':
                self.path = '/index.html'
            super().do_GET()

# 采用多线程继承类，防止单线程被 IO (如 sqlite 读写) 锁死导致网页无响应
class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def server_bind(self):
        """覆写 server_bind，跳过 socket.getfqdn() 反向 DNS 查询。
        
        原因：Python 标准库 HTTPServer.server_bind() 会调用 socket.getfqdn(host)
        进行反向 DNS 解析，在某些网络环境下该调用会超时 30 秒，导致服务器启动极慢。
        这里直接绑定 socket 并手动设置 server_name，完全绕过 DNS 查询。
        """
        self.socket.setsockopt(__import__('socket').SOL_SOCKET, __import__('socket').SO_REUSEADDR, 1)
        self.socket.bind(self.server_address)
        self.server_address = self.socket.getsockname()
        # 直接硬编码 server_name，不走 getfqdn
        host, port = self.server_address[:2]
        self.server_name = host or 'localhost'
        self.server_port = port

def main():
    server_address = ('127.0.0.1', PORT)
    httpd = ThreadingHTTPServer(server_address, TokenMonitorHandler)
    print(f"[+] Token Monitor 实时仪表盘已开启: http://127.0.0.1:{PORT}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n[-] 正在关闭 Web 服务器...")
        httpd.server_close()

if __name__ == '__main__':
    main()
