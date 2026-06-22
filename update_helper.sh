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
#   * 不依赖 GUI, 纯命令行。
#   * 失败时打印明确错误到 /tmp/token_monitor_update.log, 不静默。
#   * 使用 disown + nohup 守护自身。
set -u

STAGED_APP="$1"
TARGET_APP="$2"
RELAUNCH_BUNDLE_ID="$3"
LOG_FILE="/tmp/token_monitor_update.log"
PID_TO_WAIT="$$"  # 当前进程不需要 wait, 真正要等的是主 app

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

# 用 ditto 替换 .app。ditto 是 macOS 自带, 比 cp -R 更可靠地处理 .app bundle
# (保留 resource fork / extended attributes / 签名元数据)。
echo "[update_helper] 替换中: $TARGET_APP"
if ! ditto "$STAGED_APP" "$TARGET_APP"; then
    echo "[update_helper] ✘ 替换失败, 请检查 /Applications 写入权限"
    open "file://$LOG_FILE" || true
    exit 1
fi

# 重新签名 (替换路径下 ad-hoc 签名在新位置可能丢失, 显式签一次保险)
codesign --force --deep --sign - "$TARGET_APP" >/dev/null 2>&1 || true

# 重启 app。优先用 open -b 按 bundle id 启动, 失败 fallback 到 open -a 路径。
echo "[update_helper] 重启 app"
if [ -n "$RELAUNCH_BUNDLE_ID" ]; then
    open -b "$RELAUNCH_BUNDLE_ID" || open -a "$TARGET_APP" || true
else
    open -a "$TARGET_APP" || true
fi

echo "[update_helper] ✔ 完成"
exit 0
