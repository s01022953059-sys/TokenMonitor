#!/bin/bash
# Token Monitor Windows 构建脚本 (Go 交叉编译)
#
# 产出: TokenMonitor.exe (console, 双击运行, 自动开浏览器)
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
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "TokenMonitor.exe" .

EXE_SIZE=$(du -h "TokenMonitor.exe" | cut -f1)
echo "[build_windows] EXE: TokenMonitor.exe ($EXE_SIZE)"
cd "$SOURCE_ROOT"

# ─── 2. 打 ZIP 包 ───
echo "[build_windows] [2/3] 打 ZIP"
STAGE_DIR="$SOURCE_ROOT/build/windows_stage"
mkdir -p "$STAGE_DIR"
cp go_build/TokenMonitor.exe "$STAGE_DIR/TokenMonitor.exe"

cat > "$STAGE_DIR/README.txt" << 'README'
Token Monitor for Windows
=========================

双击 TokenMonitor.exe 启动
  → 弹出控制台窗口, 自动打开浏览器

手动访问: http://127.0.0.1:15723
停止服务: 关闭控制台窗口
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
echo "  产出: $ZIP_PATH"
