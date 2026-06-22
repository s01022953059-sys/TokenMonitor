#!/bin/bash
# Token Monitor 一站式安装脚本
#
# 用法: 在解压后的源码根目录下执行 bash install.sh
# 依赖: macOS + Xcode Command Line Tools + Pillow (脚本会自动检查)
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="Token Monitor"
INSTALL_PATH="/Applications/$APP_NAME.app"
BUILD_OUTPUT="$SOURCE_ROOT/build/$APP_NAME.app"

cd "$SOURCE_ROOT"

# ===== 步骤 0: 前置检查 =====
echo ""
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "[install]   Token Monitor 一站式安装"
echo "[install]   源码根: $SOURCE_ROOT"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# 检查 macOS
if [[ "$(uname)" != "Darwin" ]]; then
    echo "[install] ✘ 这个脚本只能在 macOS 上跑 (当前: $(uname))" >&2
    exit 1
fi

# 检查 swiftc
if ! command -v swiftc >/dev/null 2>&1; then
    echo "[install] ✘ 找不到 swiftc, 请先执行:" >&2
    echo "[install]     xcode-select --install" >&2
    exit 1
fi

# 检查 Pillow (提示装, 不强制 exit, 后续可装后重跑)
PILLOW_OK=0
if python3 -c "from PIL import Image" 2>/dev/null; then
    PILLOW_OK=1
    echo "[install] [✔] Pillow 已装, .icns 图标会生成"
else
    echo "[install] [!] Pillow 未装, .icns 跳过 (app 仍然能跑, 只是用占位图标)"
    echo "[install]     装 Pillow: pip3 install Pillow --break-system-packages"
fi

# ===== 步骤 1: build =====
echo ""
echo "[install] [1/5] 编译 $APP_NAME.app ..."
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
bash build_macos.sh

# ===== 步骤 2: 验证 build 产物 =====
echo ""
echo "[install] [2/5] 验证 build 产物"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [[ ! -f "$BUILD_OUTPUT/Contents/MacOS/TokenMonitor" ]]; then
    echo "[install] ✘ build 产物缺失: $BUILD_OUTPUT/Contents/MacOS/TokenMonitor" >&2
    exit 1
fi
INSTALLED_VERSION=$(plutil -extract CFBundleShortVersionString raw -o - "$BUILD_OUTPUT/Contents/Info.plist")
echo "[install] [✔] version: $INSTALLED_VERSION"
echo "[install] [✔] 可执行文件: $BUILD_OUTPUT/Contents/MacOS/TokenMonitor"
if [[ -f "$BUILD_OUTPUT/Contents/Resources/AppIcon.icns" ]]; then
    echo "[install] [✔] AppIcon.icns 已生成"
else
    echo "[install] [!] AppIcon.icns 缺失, 装上后是占位图标 (装 Pillow 后重新跑 build_macos.sh 修复)"
fi

# ===== 步骤 3: 退出当前 app =====
echo ""
echo "[install] [3/5] 退出当前 $APP_NAME 实例"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
pkill -f "Token Monitor" 2>/dev/null || true
osascript -e "quit app \"$APP_NAME\"" 2>/dev/null || true
sleep 2
echo "[install] [✔] 旧进程已清理"

# ===== 步骤 4: 替换 .app (sudo) =====
echo ""
echo "[install] [4/5] 替换 $INSTALL_PATH (需要 sudo 密码)"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [[ ! -d "$INSTALL_PATH" ]]; then
    echo "[install] [!] 第一次安装, $INSTALL_PATH 不存在, 跳过 rm"
fi
sudo rm -rf "$INSTALL_PATH"
sudo cp -R "$BUILD_OUTPUT" "$INSTALL_PATH"
# 去 quarantine, 避免 Gatekeeper 拦截
sudo xattr -dr com.apple.quarantine "$INSTALL_PATH" 2>/dev/null || true
echo "[install] [✔] $INSTALL_PATH 已就位"

# ===== 步骤 5: 启动 =====
echo ""
echo "[install] [5/5] 启动新版本"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
open "$INSTALL_PATH"
echo "[install] [✔] 已发送 open 命令"

echo ""
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "[install]   安装完成!"
echo "[install]   版本: $INSTALLED_VERSION"
echo "[install]   大屏左上角应该显示 v$INSTALLED_VERSION"
echo "[install] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"