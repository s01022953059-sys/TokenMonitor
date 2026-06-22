#!/bin/bash
# Token Monitor 应用内自更新的 helper 脚本
#
# 这个脚本由主 app 在退出前启动 (Process.run, sh -c),
# 等主 app 完全退出后, 替换 /Applications/Token Monitor.app 并重启。
# 它必须独立于主 app 进程运行, 否则在主 app 退出时会被 SIGHUP 干掉。
#
# 用法 (主 app 调用):
#   update_helper.sh <staged-app-path> <target-app-path> <relaunch-bundle-id>
#   - staged-app-path:  已经过 build_macos.sh 编译 + 签名的新 .app
#   - target-app-path:  通常是 "/Applications/Token Monitor.app"
#   - relaunch-bundle-id: 通常是 "com.baggio.tokenmonitor"
#
# 设计原则:
#   * /Applications/ 写入需要 root, helper 进程本身没有 sudo, 用 osascript
#     触发系统 GUI sudo 弹窗 (Touch ID / 密码) 拿到临时 root 权限。
#   * 失败时打印明确错误到 /tmp/token_monitor_update.log, 不静默。
#   * 使用 disown + nohup 守护自身。
set -u

STAGED_APP="$1"
TARGET_APP="$2"
RELAUNCH_BUNDLE_ID="$3"
LOG_FILE="/tmp/token_monitor_update.log"

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

# /Applications/ 写入需要 root。helper 进程本身没有 sudo 权限,
# 但可以用 osascript 触发系统 GUI sudo 弹窗, 用户输密码后 helper
# 拿到临时 root 权限做替换。
#
# AppleScript 的 'do shell script ... with administrator privileges' 会:
#   - 弹一个系统级认证对话框 (类似安装 app 时的认证)
#   - 用户用 Touch ID / 密码授权后, 整个 script 在 root 下执行
#   - 失败 (用户取消 / 密码错) 返回非 0
echo "[update_helper] 请求管理员权限..."
AS_SCRIPT=$(cat <<EOS
do shell script "
    set -e
    TARGET_APP='$TARGET_APP'
    STAGED_APP='$STAGED_APP'
    echo '[as-root] 删除旧 \$TARGET_APP'
    rm -rf \"\$TARGET_APP\"
    echo '[as-root] ditto 替换'
    ditto \"\$STAGED_APP\" \"\$TARGET_APP\"
    echo '[as-root] 重新签名'
    codesign --force --deep --sign - \"\$TARGET_APP\" 2>&1 || true
    echo '[as-root] 完成'
" with administrator privileges with prompt "Token Monitor 需要管理员权限以替换 /Applications/Token Monitor.app"
EOS
)

if ! osascript -e "$AS_SCRIPT"; then
    echo "[update_helper] ✘ 用户取消授权或认证失败"
    open "file://$LOG_FILE" || true
    exit 1
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