#!/bin/bash
# Token Monitor 应用内自更新的 helper 脚本
#
# 这个脚本由主 app 在退出前启动 (Process.run, sh -c),
# 等主 app 完全退出后, 替换 .app 并重启。
# 它必须独立于主 app 进程运行, 否则在主 app 退出时会被 SIGHUP 干掉。
#
# 用法 (主 app 调用):
#   update_helper.sh <staged-app-path> <target-app-path> <relaunch-bundle-id>
#
# 设计原则:
#   * 目标目录可写时直接替换，不因位于 /Applications 就请求管理员权限
#   * 目标目录不可写时迁移到 ~/Applications，后续更新保持静默
#   * 全程不调用系统管理员授权
#   * 失败时记录明确错误到 /tmp/token_monitor_update.log
set -u

STAGED_APP="$1"
TARGET_APP="$2"
RELAUNCH_BUNDLE_ID="$3"
LOG_FILE="/tmp/token_monitor_update.log"
USER_APPLICATIONS_DIR="${TOKEN_MONITOR_USER_APPLICATIONS_DIR:-$HOME/Applications}"
TEST_MODE="${TOKEN_MONITOR_UPDATE_TEST_MODE:-0}"
FORCE_TARGET_UNWRITABLE="${TOKEN_MONITOR_FORCE_TARGET_UNWRITABLE:-0}"

# 重定向所有输出到日志, 这样即使 helper 后台跑用户也能看到结果。
exec >> "$LOG_FILE" 2>&1
echo ""
echo "[update_helper] $(date) 启动"
echo "[update_helper] staged=$STAGED_APP"
echo "[update_helper] target=$TARGET_APP"

if [ ! -d "$STAGED_APP" ]; then
    echo "[update_helper] ✘ 暂存目录不存在: $STAGED_APP"
    open "file://$LOG_FILE" || true
    exit 1
fi

# 主 app 退出后, LaunchServices 可能还有缓存指向旧 .app 的可执行文件句柄。
# 给 3 秒缓冲, 确保旧进程真的被回收。
if [ "$TEST_MODE" != "1" ]; then
    sleep 3
fi

# 可写则原地替换；不可写则迁移到用户 Applications。
ORIGINAL_TARGET_APP="$TARGET_APP"
TARGET_DIR="$(dirname "$TARGET_APP")"
if [ "$FORCE_TARGET_UNWRITABLE" = "1" ] || [ ! -w "$TARGET_DIR" ]; then
    TARGET_APP="$USER_APPLICATIONS_DIR/$(basename "$TARGET_APP")"
    TARGET_DIR="$USER_APPLICATIONS_DIR"
    echo "[update_helper] 原目录不可写，迁移到用户目录: $TARGET_APP"
fi

mkdir -p "$TARGET_DIR"
rm -rf "$TARGET_APP"
if ! ditto "$STAGED_APP" "$TARGET_APP"; then
    echo "[update_helper] ✘ ditto 替换失败: $TARGET_APP"
    [ "$TEST_MODE" = "1" ] || open "file://$LOG_FILE" || true
    exit 1
fi
codesign --force --deep --sign - "$TARGET_APP" 2>&1 || true
echo "[update_helper] ✔ 静默替换完成: $TARGET_APP"

# 从不可写系统目录迁移后，注销旧副本，避免 LaunchServices 显示两个同名 App。
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
if [ "$ORIGINAL_TARGET_APP" != "$TARGET_APP" ] && [ -x "$LSREGISTER" ]; then
    "$LSREGISTER" -u "$ORIGINAL_TARGET_APP" 2>/dev/null || true
fi

if [ "$TEST_MODE" = "1" ]; then
    echo "[update_helper] ✔ 测试模式完成"
    exit 0
fi

# 重启 app。彻底清理老进程 (Swift 主进程 + Python server 子进程),
# 释放端口和 lock 文件, 再 open 拉新的。
# 之前只 pkill bundle id, Python server 子进程残留, 端口 15723 被占,
# lock 文件没释放, 导致新 app 启动时 server 绑端口失败, 整个 app 起不来。
# v1.3.84 修复: 还跑 lsregister 重置 NSStatusItem 缓存 — 旧 app 进程如果还活着,
# status item 的 image/title 是 cache 在老 Swift binary 里的, 不会自动刷。
# 必须让老进程彻底死 + lsregister 重注册, 新 app 启动才会走 `button.title = "🔥"`。
echo "[update_helper] 重启 app (彻底清理老进程 + 重置 NSStatusItem 缓存)"

# 1. kill Swift 主进程: 用 lsof 查 .app 二进制路径, 直接 kill by pid
#    pkill -f "TokenMonitor" 太宽 (会误杀 Token Monitor.app 内任何 exec),
#    pkill -f "com.baggio.tokenmonitor" 在 mac 上 bundle id 不在命令行里
#    → 必须按可执行文件路径精确杀
TOKEN_MONITOR_BIN="$TARGET_APP/Contents/MacOS/TokenMonitor"
PIDS=$(lsof -t "$TOKEN_MONITOR_BIN" 2>/dev/null | tr '\n' ' ')
if [ -n "$PIDS" ]; then
    echo "[update_helper] kill 老 Swift 主进程: $PIDS"
    kill $PIDS 2>/dev/null || true
    sleep 1
    # 没死透的强杀
    PIDS=$(lsof -t "$TOKEN_MONITOR_BIN" 2>/dev/null | tr '\n' ' ')
    if [ -n "$PIDS" ]; then
        echo "[update_helper] 老进程不响应, 强杀: $PIDS"
        kill -9 $PIDS 2>/dev/null || true
    fi
fi
# 兜底: pkill (可能漏的, 比如 lsregister launchd 启动的 ghost app)
pkill -f "TokenMonitor.app/Contents/MacOS/TokenMonitor" 2>/dev/null || true
pkill -x TokenMonitor 2>/dev/null || true

# 2. kill Python server 子进程 (按 server.py 路径匹配)
pkill -f "Token Monitor.*server\.py" 2>/dev/null || true
pkill -f "token_monitor.*server\.py" 2>/dev/null || true

# 3. 等老进程完全退出, 端口释放
sleep 2

# 4. 清理 lock 文件, 防止新 server 认为已有实例在跑
rm -f /tmp/token_monitor_server.lock

# 5. 确认端口已释放 (最多等 3 秒)
for i in 1 2 3; do
    if ! lsof -i :15723 >/dev/null 2>&1; then
        break
    fi
    echo "[update_helper] 端口 15723 仍被占用, 等待... ($i/3)"
    sleep 1
    # 强制 kill 占用端口的进程
    lsof -ti :15723 2>/dev/null | xargs kill -9 2>/dev/null || true
done

# 5b. 重置 macOS NSStatusItem / LaunchServices 缓存
# 老 app 进程的 status item (icon/title) 已经被 system 加到全局状态栏,
# 只重启 Swift binary 不够, 老 item 可能残留 0.x 秒
# 强制 lsregister 重索引 (跟 install.sh / build_macos.sh 同步), 触发系统重读 .app
if [ -x "$LSREGISTER" ]; then
    "$LSREGISTER" -f -R -trusted "$TARGET_APP" 2>/dev/null && \
        echo "[update_helper] ✔ lsregister 重索引 (清 NSStatusItem 缓存)" || true
fi
# touch Info.plist 触发 LaunchServices 重新读 icon 缓存
touch "$TARGET_APP/Contents/Info.plist" 2>/dev/null || true

# 6. 启动新 app
# 必须按路径启动；按 bundle id 启动可能重新打开遗留的系统目录副本。
open "$TARGET_APP" || open -a "$TARGET_APP" || true

echo "[update_helper] ✔ 完成"
exit 0
