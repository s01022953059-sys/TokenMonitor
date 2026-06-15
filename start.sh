#!/bin/bash

# 显式补全 PATH，确保在 GUI 启动、LaunchAgents 等无终端环境下也能顺利找到 python3 二进制
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

# 获取脚本所在的绝对路径
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
# PID 文件放在 /tmp 下，因为 .app/Contents/Resources/ 在某些 macOS 场景下是只读的
PID_FILE="/tmp/token_monitor_server.pid"
PORT=15723

case "$1" in
    start)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            # 不仅要求 PID 存活，还要验证该 PID 对应的确实是 Python 进程，防止 PID 碰撞误判
            if ps -p $PID > /dev/null && ps -p $PID -o command= | grep -qi "python"; then
                echo "[!] Token Monitor 服务已在后台稳定运行中 (PID: $PID)"
                exit 0
            fi
            rm "$PID_FILE"
        fi
        
        echo "[*] 正在后台启动 Token Monitor 统计服务..."
        # 后台启动 python 服务器，重定向输出以保持终端干净
        python3 "$DIR/server.py" > /dev/null 2>&1 &
        NEW_PID=$!
        echo $NEW_PID > "$PID_FILE"
        sleep 1.5
        
        if ps -p $NEW_PID > /dev/null; then
            echo "[✔] 启动成功！(PID: $NEW_PID)"
        else
            echo "[✘] 启动失败。请确认 127.0.0.1:$PORT 端口未被其他服务占用。"
            [ -f "$PID_FILE" ] && rm "$PID_FILE"
        fi
        ;;
        
    stop)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if ps -p $PID > /dev/null; then
                echo "[*] 正在停止 Token Monitor 服务 (PID: $PID)..."
                kill $PID
                echo "[✔] 服务已成功终止。"
            else
                echo "[-] PID 文件存在，但未检测到相关活跃进程。"
            fi
            rm "$PID_FILE"
        else
            # 兜底机制：通过 lsof 直接释放端口
            PID_PORT=$(lsof -t -i:$PORT 2>/dev/null)
            if [ ! -z "$PID_PORT" ]; then
                echo "[*] 正在清理端口 $PORT 占用的进程 (PID: $PID_PORT)..."
                kill $PID_PORT
                echo "[✔] 端口已释放。"
            else
                echo "[-] 未检测到有运行中的服务。"
            fi
        fi
        ;;
        
    status)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if ps -p $PID > /dev/null; then
                echo "[●] 状态: 运行中 (PID: $PID)"
                echo "[*] 访问地址: http://127.0.0.1:$PORT"
                exit 0
            fi
        fi
        
        PID_PORT=$(lsof -t -i:$PORT 2>/dev/null)
        if [ ! -z "$PID_PORT" ]; then
            echo "[●] 状态: 活跃中 (直接占用端口 $PORT, PID: $PID_PORT)"
        else
            echo "[○] 状态: 已停止"
        fi
        ;;
        
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
        
    *)
        echo "使用方法: $0 {start|stop|status|restart}"
        exit 1
        ;;
esac
