#!/bin/bash
# Token Monitor Windows 构建脚本 (Go 交叉编译)
#
# 产出: build/TokenMonitor-Setup.exe（正式安装与应用内更新）
# v1.3.95: 合并 launcher, 只编一个 exe, -H windowsgui 隐藏 cmd 窗口
#
# 用法: bash build_windows.sh
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"
mkdir -p "$SOURCE_ROOT/build"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')

echo "[build_windows] 版本: $APP_VERSION"

# ─── 1. 交叉编译 ───
echo "[build_windows] [1/2] Go 交叉编译主程序 (windows/amd64)"
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

# ─── 2. 生成用户级安装程序 ───
echo "[build_windows] [2/2] 生成用户级安装程序"
cd go_build
INSTALLER_DIR="cmd/installer"
mkdir -p "$INSTALLER_DIR/payload"
cp TokenMonitor.exe "$INSTALLER_DIR/payload/TokenMonitor.exe"
[ -f rsrc.syso ] && cp rsrc.syso "$INSTALLER_DIR/rsrc.syso"
GOOS=windows GOARCH=amd64 go build \
  -ldflags="-H windowsgui -s -w -X main.version=$APP_VERSION" \
  -o "$SOURCE_ROOT/build/TokenMonitor-Setup.exe" "./$INSTALLER_DIR"
SETUP_SIZE=$(du -h "$SOURCE_ROOT/build/TokenMonitor-Setup.exe" | cut -f1)
echo "[build_windows]   TokenMonitor-Setup.exe ($SETUP_SIZE)"

rm -f "$INSTALLER_DIR/payload/TokenMonitor.exe" "$INSTALLER_DIR/rsrc.syso"
rm -f TokenMonitor.exe version.txt

echo "[build_windows] ✔ 完成"
echo "  SETUP: $SOURCE_ROOT/build/TokenMonitor-Setup.exe"
