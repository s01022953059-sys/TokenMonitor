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
#   * ~/Applications/ 用户目录: 直接 cp, 不需要 root, 不需要 sudo, silent
#   * /Applications/ 系统目录: 用 osascript 触发系统 GUI sudo 弹窗拿 root
#   * AppleScript 写到 .scpt 文件而非 -e inline, 避开 bash heredoc 转义陷阱
#   * 失败时打印明确错误到 /tmp/token_monitor_update.log
set -u

STAGED_APP="$1"
TARGET_APP="$2"
RELAUNCH_BUNDLE_ID="$3"
LOG_FILE="/tmp/token_monitor_update.log"
SCPT_FILE="/tmp/tm_install.scpt"

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
sleep 3

# 检测 target 在系统目录还是用户目录。
# 系统目录 (/Applications/) 写入需要 root, 走 osascript sudo 弹窗;
# 用户目录 (~/Applications/) 写入不需要 root, 直接 cp (silent, 无弹窗)。
TARGET_DIR="$(dirname "$TARGET_APP")"
case "$TARGET_DIR" in
    /Applications|/Applications/*)
        INSTALL_MODE="system"
        ;;
    "$HOME"/Applications|"$HOME"/Applications/*)
        INSTALL_MODE="user"
        ;;
    *)
        # 兜底: 走系统路径, 弹 sudo
        INSTALL_MODE="system"
        ;;
esac
echo "[update_helper] install mode: $INSTALL_MODE"

if [ "$INSTALL_MODE" = "user" ]; then
    # 用户目录直接替换, 不需要 root, silent
    echo "[update_helper] 用户目录, 直接替换 (silent)"
    mkdir -p "$TARGET_DIR"
    rm -rf "$TARGET_APP"
    if ! ditto "$STAGED_APP" "$TARGET_APP"; then
        echo "[update_helper] ✘ ditto 替换失败: $TARGET_APP"
        open "file://$LOG_FILE" || true
        exit 1
    fi
    codesign --force --deep --sign - "$TARGET_APP" 2>&1 || true
    echo "[update_helper] ✔ 用户目录替换完成"
else
    # 系统目录: 写 AppleScript, 触发 osascript sudo 弹窗
    cat > "$SCPT_FILE" <<'SCPT_EOF'
on run argv
    set targetApp to item 1 of argv
    set stagedApp to item 2 of argv
    do shell script "rm -rf " & quoted form of targetApp & " && ditto " & quoted form of stagedApp & " " & quoted form of targetApp & " && codesign --force --deep --sign - " & quoted form of targetApp with administrator privileges with prompt "Token Monitor 需要管理员权限以替换 /Applications/Token Monitor.app"
end run
SCPT_EOF

    echo "[update_helper] 请求管理员权限 (AppleScript file: $SCPT_FILE) ..."
    if ! osascript "$SCPT_FILE" "$TARGET_APP" "$STAGED_APP"; then
        echo "[update_helper] ✘ 用户取消授权或认证失败"
        rm -f "$SCPT_FILE"
        open "file://$LOG_FILE" || true
        exit 1
    fi
    rm -f "$SCPT_FILE"
    echo "[update_helper] ✔ 系统目录替换完成"
fi

# 重启 app。优先用 open -b 按 bundle id 启动, 失败 fallback 到 open -a 路径。
echo "[update_helper] 重启 app"
if [ -n "$RELAUNCH_BUNDLE_ID" ]; then
    open -b "$RELAUNCH_BUNDLE_ID" || open -a "$TARGET_APP" || true
else
    open -a "$TARGET_APP" || true
fi

echo "[update_helper] ✔ 完成"
exit 0
