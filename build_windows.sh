#!/bin/bash
# Token Monitor Windows 构建脚本 (Go 交叉编译)
#
# 产出: build/TokenMonitor.exe（应用内更新）+ Windows ZIP（手动下载）
# v1.3.95: 合并 launcher, 只编一个 exe, -H windowsgui 隐藏 cmd 窗口
#
# 用法: bash build_windows.sh
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"
mkdir -p "$SOURCE_ROOT/build"
cp go_build/TokenMonitor.exe "$SOURCE_ROOT/build/TokenMonitor.exe"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
ZIP_NAME="TokenMonitor-${APP_VERSION}-win.zip"

echo "[build_windows] 版本: $APP_VERSION"

# ─── 1. 交叉编译 ───
echo "[build_windows] [1/3] Go 交叉编译 (windows/amd64)"
cd go_build
echo "$APP_VERSION" > version.txt
# 同步主目录 index.html / chart.js 到 go_build/static
cp ../index.html static/index.html
[ -f ../chart.js ] && cp ../chart.js static/chart.js

# v1.3.97: 单 exe, -H windowsgui 隐藏 cmd 窗口
# -X main.appVersion 注入版本号 (不依赖 version.txt, 修复更新检测失败)
# rsrc.syso (icon.ico) 自动被 go build 链接, 给 exe 加图标
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui -s -w -X main.appVersion=$APP_VERSION" -o "TokenMonitor.exe" .
EXE_SIZE=$(du -h "TokenMonitor.exe" | cut -f1)
echo "[build_windows]   TokenMonitor.exe ($EXE_SIZE)"

cd "$SOURCE_ROOT"

# ─── 2. 打 ZIP 包 ───
echo "[build_windows] [2/3] 打 ZIP"
STAGE_DIR="$SOURCE_ROOT/build/windows_stage"
mkdir -p "$STAGE_DIR"
cp go_build/TokenMonitor.exe "$STAGE_DIR/TokenMonitor.exe"
cat > "$STAGE_DIR/README.txt" << 'README'
Token Monitor for Windows
=========================

双击 TokenMonitor.exe 启动:
  → 弹出独立 WebView2 窗口显示仪表盘 (不打开你的浏览器)
  → 关闭窗口后应用继续留在系统托盘
  → 托盘“开机自启”会在登录后静默启动
  → 所有更新状态和下载进度都在“关于”窗口显示

手动访问: http://127.0.0.1:15723
停止服务: 任务管理器结束 TokenMonitor.exe 进程
首次运行需要绕过 SmartScreen (详见 README.md "绕过安全限制")
README

cd "$STAGE_DIR"
zip -r "$SOURCE_ROOT/build/$ZIP_NAME" . -x ".*"
rm -rf "$STAGE_DIR"

ZIP_PATH="$SOURCE_ROOT/build/$ZIP_NAME"
ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
echo "[build_windows] ZIP: $ZIP_PATH ($ZIP_SIZE)"

# ─── 3. 清理 ───
echo "[build_windows] [3/3] 清理"
rm -f go_build/TokenMonitor.exe go_build/version.txt

echo "[build_windows] ✔ 完成"
echo "  EXE: $SOURCE_ROOT/build/TokenMonitor.exe"
echo "  ZIP: $ZIP_PATH"
