#!/bin/bash
# Token Monitor 本地服务启动脚本
#
# 用法:
#   start.sh start  [PORT] [UPDATE_FEED_URL]
#   start.sh stop   [PORT]
#   start.sh status [PORT]
#   start.sh restart [PORT] [UPDATE_FEED_URL]
#
# PORT / UPDATE_FEED_URL 都是可选的, 没传时使用默认值:
#   * PORT 默认 15723
#   * UPDATE_FEED_URL 默认空 (server.py 内部会按 "未配置" 处理)
#
# Swift 启动器从 Info.plist 读出 TokenMonitorAPIPort 和 TokenMonitorUpdateFeedURL
# 然后透传给本脚本, 因此所有配置只有 Info.plist 一个真源。

export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH}"

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PID_FILE="/tmp/token_monitor_server.pid"
# 单实例锁文件路径: 与 server.py 的 SINGLETON_LOCK_PATH 保持一致,
# start.sh 和 server.py 共享同一把 flock, 谁先拿到谁先用。
SINGLETON_LOCK_FILE="/tmp/token_monitor_server.lock"
PORT="${2:-15723}"
UPDATE_FEED_URL="${3:-}"

# 后向兼容: 老的 .app 二进制只会传 2 个参数 (start, port), 这里在第 3 个参数为空时,
# 主动到 Info.plist 拿 TokenMonitorUpdateFeedURL, 保证 server.py 不会丢掉更新源配置。
if [ -z "$UPDATE_FEED_URL" ]; then
    for plist in "$DIR/../Info.plist" "$DIR/Info.plist" "$DIR/../../Info.plist"; do
        if [ -f "$plist" ]; then
            CANDIDATE=$(plutil -extract TokenMonitorUpdateFeedURL raw -o - "$plist" 2>/dev/null || true)
            if [ -n "$CANDIDATE" ] && [ "$CANDIDATE" != "<no value>" ]; then
                UPDATE_FEED_URL="$CANDIDATE"
                break
            fi
        fi
    done
fi

case "$1" in
    start)
        # 单实例闸门: _singleton_check.py 走 fcntl.flock, 跟 server.py 用同一把锁。
        # 锁被占时先确认持锁进程是否真的在监听端口; 如果是僵尸/卡死进程,
        # 强杀并清锁后重试一次, 避免空锁或死锁进程永久挡住启动。
        if ! python3 "$DIR/_singleton_check.py" "$SINGLETON_LOCK_FILE" 2>/dev/null; then
            STALE_PID=""
            if [ -f "$PID_FILE" ]; then
                STALE_PID=$(cat "$PID_FILE" 2>/dev/null)
            fi
            # 持锁进程活着且在监听 → 真的在运行, 放弃启动
            if [ -n "$STALE_PID" ] && ps -p "$STALE_PID" > /dev/null 2>&1; then
                if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -a -p "$STALE_PID" > /dev/null 2>&1; then
                    echo "[!] Token Monitor 服务已在运行 (PID: $STALE_PID), 启动请求被忽略。"
                    exit 0
                fi
                # 进程活着但没监听端口 → 卡死了, 强杀
                echo "[*] 检测到卡死的服务进程 (PID: $STALE_PID 未监听端口), 正在清理..."
                kill -9 "$STALE_PID" 2>/dev/null || true
            fi
            # 清掉陈旧的锁和 pid, 重试一次
            rm -f "$SINGLETON_LOCK_FILE" "$PID_FILE" 2>/dev/null || true
            if ! python3 "$DIR/_singleton_check.py" "$SINGLETON_LOCK_FILE" 2>/dev/null; then
                echo "[!] 无法获取单实例锁 $SINGLETON_LOCK_FILE, 启动请求被忽略。"
                exit 0
            fi
        fi
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if ps -p "$PID" > /dev/null 2>&1 && ps -p "$PID" -o command= | grep -qi "python"; then
                echo "[!] Token Monitor 服务已在后台稳定运行中 (PID: $PID)"
                exit 0
            fi
            rm "$PID_FILE"
        fi

        echo "[*] 正在后台启动 Token Monitor 统计服务..."
        # nohup + disown 让 server 脱离 start.sh 的进程组,
        # 这样 Swift Process.run() 退出时不会用 SIGHUP 杀掉 server。
        if [ -n "$UPDATE_FEED_URL" ]; then
            nohup python3 "$DIR/server.py" --port "$PORT" --update-feed-url "$UPDATE_FEED_URL" > /dev/null 2>&1 &
        else
            nohup python3 "$DIR/server.py" --port "$PORT" > /dev/null 2>&1 &
        fi
        NEW_PID=$!
        disown $NEW_PID 2>/dev/null || true
        echo $NEW_PID > "$PID_FILE"
        sleep 1.5

        if ps -p $NEW_PID > /dev/null 2>&1; then
            echo "[✔] 启动成功! (PID: $NEW_PID)"
        else
            echo "[✘] 启动失败。请确认 127.0.0.1:$PORT 端口未被其他服务占用。"
            [ -f "$PID_FILE" ] && rm "$PID_FILE"
        fi
        ;;

    stop)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if ps -p "$PID" > /dev/null 2>&1; then
                echo "[*] 正在停止 Token Monitor 服务 (PID: $PID)..."
                kill "$PID"
                echo "[✔] 服务已成功终止。"
            else
                echo "[-] PID 文件存在,但未检测到相关活跃进程。"
            fi
            rm "$PID_FILE"
        else
            PID_PORT=$(lsof -t -i:$PORT 2>/dev/null)
            if [ -n "$PID_PORT" ]; then
                echo "[*] 正在清理端口 $PORT 占用的进程 (PID: $PID_PORT)..."
                kill "$PID_PORT"
                echo "[✔] 端口已释放。"
            else
                echo "[-] 未检测到有运行中的服务。"
            fi
        fi
        ;;

    status)
        if [ -f "$PID_FILE" ]; then
            PID=$(cat "$PID_FILE")
            if ps -p "$PID" > /dev/null 2>&1; then
                echo "[●] 状态: 运行中 (PID: $PID)"
                echo "[*] 访问地址: http://127.0.0.1:$PORT"
                exit 0
            fi
        fi
        PID_PORT=$(lsof -t -i:$PORT 2>/dev/null)
        if [ -n "$PID_PORT" ]; then
            echo "[●] 状态: 活跃中 (直接占用端口 $PORT, PID: $PID_PORT)"
        else
            echo "[○] 状态: 已停止"
        fi
        ;;

    restart)
        "$0" stop "$PORT"
        sleep 1
        if [ -n "$UPDATE_FEED_URL" ]; then
            "$0" start "$PORT" "$UPDATE_FEED_URL"
        else
            "$0" start "$PORT"
        fi
        ;;

    *)
        echo "用法: $0 {start|stop|status|restart} [PORT] [UPDATE_FEED_URL]"
        exit 1
        ;;
esac
