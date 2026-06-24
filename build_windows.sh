#!/bin/bash
# Token Monitor Windows EXE 构建脚本
#
# 两种模式:
#   1. 本地 Windows: 直接用 PyInstaller 打包
#   2. macOS 交叉: 用 Wine + PyInstaller (实验性)
#
# 推荐在 Windows 上运行此脚本 (或 PowerShell 等效命令)。
# macOS 上也可以跑, 会用 Docker Windows 容器。
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/' || echo "1.3.44")
ZIP_NAME="TokenMonitor-${APP_VERSION}-win.zip"
TAG="v${APP_VERSION}"

echo "[build_windows] 版本: $APP_VERSION"
echo "[build_windows] 平台: $(uname)"

# ─── 检测环境 ───
if [[ "$(uname)" == "Darwin" ]]; then
    # macOS: 尝试用 Docker Windows 容器打包
    if ! command -v docker &>/dev/null; then
        echo "[build_windows] ✘ macOS 上构建 Windows EXE 需要 Docker"
        echo "  请安装 Docker Desktop 并启用 Windows 容器, 或在 Windows 机器上运行此脚本"
        echo ""
        echo "  在 Windows 上的步骤:"
        echo "  1. 安装 Python 3.10+ from python.org"
        echo "  2. pip install pyinstaller"
        echo "  3. bash build_windows.sh  (或 python -m PyInstaller token_monitor.spec --noconfirm)"
        exit 1
    fi
    echo "[build_windows] 使用 Docker 打包 Windows EXE..."
    # TODO: Docker Windows 容器方案 (需要 Windows 容器支持)
    echo "[build_windows] Docker Windows 容器方案需要 Windows 主机, 跳过"
    echo "[build_windows] 请在 Windows 机器上运行此脚本"
    exit 1

elif [[ "$(uname)" == *"MINGW"* ]] || [[ "$(uname)" == *"MSYS"* ]] || [[ "$(uname)" == "CYGWIN_NT"* ]]; then
    # Windows (Git Bash / MSYS2)
    echo "[build_windows] [1/3] 安装 PyInstaller"
    pip install pyinstaller 2>/dev/null || pip3 install pyinstaller

    echo "[build_windows] [2/3] PyInstaller 打包"
    python -m PyInstaller token_monitor.spec --noconfirm \
        --distpath "$SOURCE_ROOT/build/windows" \
        --workpath "$SOURCE_ROOT/build/windows_build"

    EXE_PATH="$SOURCE_ROOT/build/windows/TokenMonitor.exe"
    if [[ ! -f "$EXE_PATH" ]]; then
        echo "[build_windows] ✘ EXE 没生成: $EXE_PATH" >&2
        exit 1
    fi

    EXE_SIZE=$(du -h "$EXE_PATH" | cut -f1)
    echo "[build_windows] EXE: $EXE_PATH ($EXE_SIZE)"

    # 复制 chart.js 到 dist 目录 (PyInstaller spec 已通过 datas 打包, 但保险起见)
    echo "[build_windows] [3/3] 打 ZIP"
    STAGE_DIR="$SOURCE_ROOT/build/windows/stage"
    mkdir -p "$STAGE_DIR"
    cp "$EXE_PATH" "$STAGE_DIR/TokenMonitor.exe"

    # 创建启动说明
    cat > "$STAGE_DIR/README.txt" << 'README'
Token Monitor for Windows
=========================

双击 TokenMonitor.exe 启动, 浏览器会自动打开仪表盘。

如需手动访问: http://127.0.0.1:15723

停止服务: 在任务管理器中结束 TokenMonitor.exe 进程
README

    cd "$STAGE_DIR"
    zip -r "$SOURCE_ROOT/build/$ZIP_NAME" . -x ".*"
    rm -rf "$STAGE_DIR"

    ZIP_PATH="$SOURCE_ROOT/build/$ZIP_NAME"
    ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
    echo "[build_windows] ZIP: $ZIP_PATH ($ZIP_SIZE)"
    echo "[build_windows] ✔ 完成"

else
    # Linux 或其他
    echo "[build_windows] ✘ 不支持的平台: $(uname)" >&2
    echo "  请在 Windows 上运行此脚本" >&2
    exit 1
fi
