#!/bin/bash
# Token Monitor Windows 构建脚本 (Go 交叉编译)
#
# 产出:
#   - TokenMonitor.exe        (console, 后台 HTTP 服务, 不开浏览器)
#   - TokenMonitorLauncher.exe (GUI, WebView2 嵌 UI, 推荐用户用这个启动)
#
# 用法: bash build_windows.sh
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
ZIP_NAME="TokenMonitor-${APP_VERSION}-win.zip"

echo "[build_windows] 版本: $APP_VERSION"

# ─── 1. 交叉编译 ───
echo "[build_windows] [1/3] Go 交叉编译 (windows/amd64)"
cd go_build
echo "$APP_VERSION" > version.txt
# 同步主目录 index.html / chart.js 到 go_build/static (//go:embed 在 build 时
# 把 go_build/static/* 嵌进 EXE, 必须 build 前保持同步, 否则 win 端看不到
# 最近的 UI 改动)。先拷再 build 一次, 避免重复编译。
cp ../index.html static/index.html
[ -f ../chart.js ] && cp ../chart.js static/chart.js

# 1a. 主服务 TokenMonitor.exe (console, 后台 HTTP)
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "TokenMonitor.exe" .
EXE_SIZE=$(du -h "TokenMonitor.exe" | cut -f1)
echo "[build_windows]   TokenMonitor.exe ($EXE_SIZE)"

# 1b. Launcher TokenMonitorLauncher.exe (GUI, 内嵌 WebView2, 不开外部浏览器)
# -H windowsgui 隐藏 cmd 窗口
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui -s -w" -o "TokenMonitorLauncher.exe" ./cmd/launcher
LAUNCHER_SIZE=$(du -h "TokenMonitorLauncher.exe" | cut -f1)
echo "[build_windows]   TokenMonitorLauncher.exe ($LAUNCHER_SIZE)"

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

启动方式 (推荐):
  双击 TokenMonitorLauncher.exe
  → 弹出独立窗口显示仪表盘 (不打开你的浏览器)
  → 关闭窗口后后台服务继续跑, 再次双击 launcher 重新打开窗口

直接后台模式:
  双击 TokenMonitor.exe
  → 弹出控制台窗口, 不打开浏览器
  → 适合想用其它方式访问的场景

手动访问: http://127.0.0.1:15723
停止服务: 任务管理器结束 TokenMonitor.exe 进程
README

cd "$STAGE_DIR"
zip -r "$SOURCE_ROOT/build/$ZIP_NAME" . -x ".*"
rm -rf "$STAGE_DIR"

ZIP_PATH="$SOURCE_ROOT/build/$ZIP_NAME"
ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
echo "[build_windows] ZIP: $ZIP_PATH ($ZIP_SIZE)"

# ─── 3. 清理 ───
echo "[build_windows] [3/3] 清理"
rm -f go_build/TokenMonitor.exe go_build/TokenMonitorLauncher.exe go_build/version.txt

echo "[build_windows] ✔ 完成"
echo "  产出: $ZIP_PATH"
