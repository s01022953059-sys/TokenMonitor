#!/bin/bash
# Token Monitor Windows 构建脚本 (Go 交叉编译)
#
# 产出两个 EXE:
#   TokenMonitor.exe         — 服务程序 (console 模式, 保持运行)
#   TokenMonitorLauncher.exe — 启动器 (GUI 模式, 隐藏窗口启动服务, 用户双击这个)
#
# 用法: bash build_windows.sh
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
ZIP_NAME="TokenMonitor-${APP_VERSION}-win.zip"

echo "[build_windows] 版本: $APP_VERSION"

# ─── 1. 交叉编译 ───
echo "[build_windows] [1/3] Go 交叉编译"
cd go_build
echo "$APP_VERSION" > version.txt

# 服务程序: console 模式 (ListenAndServe 需要阻塞)
echo "  → TokenMonitor.exe (console, 服务程序)"
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "TokenMonitor.exe" .

# 启动器: GUI 模式 (无窗口, 隐藏启动服务)
echo "  → TokenMonitorLauncher.exe (GUI, 无窗口启动器)"
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H windowsgui" -o "TokenMonitorLauncher.exe" ./launcher

cd "$SOURCE_ROOT"

# ─── 2. 打 ZIP 包 ───
echo "[build_windows] [2/3] 打 ZIP"
STAGE_DIR="$SOURCE_ROOT/build/windows_stage"
mkdir -p "$STAGE_DIR"
cp go_build/TokenMonitor.exe "$STAGE_DIR/TokenMonitor.exe"
cp go_build/TokenMonitorLauncher.exe "$STAGE_DIR/TokenMonitorLauncher.exe"

cat > "$STAGE_DIR/README.txt" << 'README'
Token Monitor for Windows
=========================

双击 TokenMonitorLauncher.exe 启动 (无窗口, 后台运行)
  → 自动打开浏览器, 访问仪表盘

手动访问: http://127.0.0.1:15723
停止服务: 在任务管理器中结束 TokenMonitor.exe

调试模式: 双击 TokenMonitor.exe (弹出控制台窗口, 显示日志)
README

cd "$STAGE_DIR"
zip -r "$SOURCE_ROOT/build/$ZIP_NAME" . -x ".*"
rm -rf "$STAGE_DIR"

ZIP_PATH="$SOURCE_ROOT/build/$ZIP_NAME"
ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
echo "[build_windows] ZIP: $ZIP_PATH ($ZIP_SIZE)"

# ─── 3. 清理 ───
echo "[build_windows] [3/3] 清理临时文件"
rm -f go_build/TokenMonitor.exe go_build/TokenMonitorLauncher.exe go_build/version.txt

echo "[build_windows] ✔ 完成"
echo "  产出: $ZIP_PATH"
