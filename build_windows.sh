#!/bin/bash
# Token Monitor Windows EXE 构建脚本 (Go 交叉编译)
#
# 在 macOS 上直接交叉编译, 不需要 Windows 机器 / Wine / Docker。
# modernc.org/sqlite 是纯 Go, 无 CGO 依赖。
#
# 用法: bash build_windows.sh
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/' || echo "1.3.44")
ZIP_NAME="TokenMonitor-${APP_VERSION}-win.zip"
TAG="v${APP_VERSION}"

echo "[build_windows] 版本: $APP_VERSION"

# ─── 1. 交叉编译 Windows EXE ───
echo "[build_windows] [1/3] Go 交叉编译 (windows/amd64)"
cd go_build

# 写入版本文件 (打包后 EXE 会读同目录 version.txt)
echo "$APP_VERSION" > version.txt

GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H windowsgui" -o "TokenMonitor.exe" .

if [[ ! -f "TokenMonitor.exe" ]]; then
    echo "[build_windows] ✘ EXE 没生成" >&2
    exit 1
fi

EXE_SIZE=$(du -h "TokenMonitor.exe" | cut -f1)
echo "[build_windows] EXE: TokenMonitor.exe ($EXE_SIZE)"
cd "$SOURCE_ROOT"

# ─── 2. 打 ZIP 包 ───
echo "[build_windows] [2/3] 打 ZIP"
STAGE_DIR="$SOURCE_ROOT/build/windows_stage"
mkdir -p "$STAGE_DIR"
cp go_build/TokenMonitor.exe "$STAGE_DIR/TokenMonitor.exe"

# 启动说明
cat > "$STAGE_DIR/README.txt" << 'README'
Token Monitor for Windows
=========================

双击 TokenMonitor.exe 启动, 浏览器会自动打开仪表盘。
如未自动打开, 手动访问: http://127.0.0.1:15723

停止服务: 在任务管理器中结束 TokenMonitor.exe 进程
README

cd "$STAGE_DIR"
zip -r "$SOURCE_ROOT/build/$ZIP_NAME" . -x ".*"
rm -rf "$STAGE_DIR"

ZIP_PATH="$SOURCE_ROOT/build/$ZIP_NAME"
ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
echo "[build_windows] ZIP: $ZIP_PATH ($ZIP_SIZE)"

# ─── 3. 清理 ───
echo "[build_windows] [3/3] 清理临时文件"
rm -f go_build/TokenMonitor.exe go_build/version.txt

echo "[build_windows] ✔ 完成"
echo "  产出: $ZIP_PATH"
