#!/bin/bash
# Token Monitor macOS 本地构建脚本
#
# 用法:
#   build_macos.sh                   构建 .app 到 ./build/Token Monitor.app
#   build_macos.sh /path/to/output   构建到指定目录
#   build_macos.sh --target PATH     直接构建并替换目标 .app (供自更新使用)
#
# 设计目标: 给 Swift 端自更新流程调用, 也支持开发者手动构建。
# 自更新场景:
#   1. Swift 端把 release zip 解压到 /tmp/TokenMonitor/update-v1.x/
#   2. Swift 端 cd 到该目录, 跑 build_macos.sh --target /Applications/Token\ Monitor.app
#   3. 本脚本编译 + 拼装新 .app 到临时目录, 用 /usr/bin/python3 提供的辅助
#      (NSFileCoordinator / FileManager.replaceItemAt) 做原子替换。
#   这里只负责"拼装到 build/", 替换动作由 Swift 端自己用 FileManager 完成。
#
# 依赖: macOS + Xcode Command Line Tools (xcode-select --install)
set -euo pipefail

# 强制在源码根目录运行, 即便被 Swift 用 cd 拉过来也能正确解析路径。
SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OUTPUT_DIR="$SOURCE_ROOT/build"
TARGET_APP_PATH=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --target)
            TARGET_APP_PATH="$2"
            shift 2
            ;;
        *)
            OUTPUT_DIR="$1"
            shift
            ;;
    esac
done

if ! command -v swiftc >/dev/null 2>&1; then
    echo "[build_macos] ✘ 找不到 swiftc, 请先执行: xcode-select --install" >&2
    exit 2
fi

APP_NAME="Token Monitor"
APP_BUNDLE="$OUTPUT_DIR/$APP_NAME.app"

echo "[build_macos] 源码根: $SOURCE_ROOT"
echo "[build_macos] 输出:   $APP_BUNDLE"

# 清理旧的 build 目录, 防止残留文件被当成新的带过去。
rm -rf "$OUTPUT_DIR"
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

# 1. 编译 Swift 入口为可执行
echo "[build_macos] [*] 编译 app_wrapper.swift ..."
swiftc \
    -O \
    -o "$APP_BUNDLE/Contents/MacOS/TokenMonitor" \
    "$SOURCE_ROOT/app_wrapper.swift"

# 2. 拷贝 Info.plist
cp "$SOURCE_ROOT/Info.plist" "$APP_BUNDLE/Contents/Info.plist"

# 3. 拷贝 Resources (脚本 + 前端资源)
# 注意: Resources/ 里的文件**不**多不少, 多了是冗余 (占 .app 体积), 少了
# 运行时找不到。原则: 启动 + 运行 + 自更新必需的, 放进来。
RESOURCE_FILES=(
    "scanner.py"
    "server.py"
    "start.sh"
    "_singleton_check.py"
    "index.html"
    "chart.js"
    "update_helper.sh"  # 自更新 helper, 主 app 退出后接管替换 + 重启
    # 注意: extract_zip.py 和 icon.png **不**放这里。extract_zip.py 不用
    # (走 ditto 不用 Python); icon.png 是 master 图标, build 完生成
    # AppIcon.icns, 源文件不打包进 .app。
)
for f in "${RESOURCE_FILES[@]}"; do
    if [[ ! -f "$SOURCE_ROOT/$f" ]]; then
        echo "[build_macos] [!] 跳过缺失文件: $f" >&2
        continue
    fi
    cp "$SOURCE_ROOT/$f" "$APP_BUNDLE/Contents/Resources/$f"
done

# 3.5 生成 .icns app 图标。
# Info.plist 的 CFBundleIconFile=AppIcon 让 macOS 找 Contents/Resources/AppIcon.icns。
# 我们仓里只有 icon.png (1024x1024 master), 用 Pillow 转一份 icns 出来。
# Pillow 在 macOS 自带的 python3 通常有 (Pillow 走系统 libjpeg/libpng), 失败时
# 给出明确错误而不是静默放一个没有图标的 app。
if [[ -f "$SOURCE_ROOT/icon.png" ]]; then
    if python3 -c "from PIL import Image" 2>/dev/null; then
        python3 - "$SOURCE_ROOT/icon.png" "$APP_BUNDLE/Contents/Resources/AppIcon.icns" <<'PYEOF'
import sys
from PIL import Image
src, dst = sys.argv[1], sys.argv[2]
img = Image.open(src).convert("RGBA")
img.save(dst, format="ICNS")
PYEOF
        if [[ -f "$APP_BUNDLE/Contents/Resources/AppIcon.icns" ]]; then
            echo "[build_macos] [✔] 生成 AppIcon.icns"
        else
            echo "[build_macos] [!] AppIcon.icns 生成失败, 系统会用占位图标" >&2
        fi
    else
        echo "[build_macos] [!] Pillow 未安装, 跳过 .icns 生成 (用 pip3 install Pillow 修复)" >&2
    fi
else
    echo "[build_macos] [!] icon.png 缺失, 跳过图标生成" >&2
fi

# 3.6 生成菜单栏状态栏图标 (template image, 系统按主题自动着色)
# Swift 端用 NSImage(named:"StatusBarIcon") 加载, 必须是 alpha-only 灰度图
# 提前用 build_assets/ 里预生成的文件 (开发期间手动 python3 build_assets/make_statusbar_icon.py 即可)
if [[ -d "$SOURCE_ROOT/build_assets" ]]; then
    mkdir -p "$APP_BUNDLE/Contents/Resources"
    cp "$SOURCE_ROOT/build_assets/icon_18.png" "$APP_BUNDLE/Contents/Resources/StatusBarIcon.png" 2>/dev/null
    cp "$SOURCE_ROOT/build_assets/icon_18@2x.png" "$APP_BUNDLE/Contents/Resources/StatusBarIcon@2x.png" 2>/dev/null
    if [[ -f "$APP_BUNDLE/Contents/Resources/StatusBarIcon.png" ]]; then
        echo "[build_macos] [✔] 拷贝菜单栏图标 (StatusBarIcon)"
    fi
fi

# 4. 修正可执行权限
chmod +x "$APP_BUNDLE/Contents/MacOS/TokenMonitor"
chmod +x "$APP_BUNDLE/Contents/Resources/start.sh"
chmod +x "$APP_BUNDLE/Contents/Resources/_singleton_check.py"
chmod +x "$APP_BUNDLE/Contents/Resources/update_helper.sh"

# 5. 重新签名 (ad-hoc), 否则 Gatekeeper 会拒绝启动替换后的 app。
#    Swift 端做 FileManager.replaceItemAt 时, 新 .app 必须已经签好名。
echo "[build_macos] [*] ad-hoc 签名 ..."
codesign --force --deep --sign - "$APP_BUNDLE" >/dev/null 2>&1

# 6. 自检: 读出新 Info.plist 的 CFBundleShortVersionString 并打印
INSTALLED_VERSION=$(plutil -extract CFBundleShortVersionString raw -o - "$APP_BUNDLE/Contents/Info.plist")
echo "[build_macos] ✔ 构建完成: $APP_BUNDLE (version: $INSTALLED_VERSION)"

# 7. 如果指定了 --target, 替换目标 .app。
#    替换不在这里做, 因为运行中的 app 不能被原地替换; Swift 端会做
#    "退出 app → 替换 → 重启" 的流程。本脚本只负责产出 build/Token Monitor.app。
if [[ -n "$TARGET_APP_PATH" ]]; then
    echo "[build_macos] [*] --target 已指定 ($TARGET_APP_PATH), 但替换动作需要 Swift 端配合, 请调用方执行 FileManager.replaceItemAt"
    echo "[build_macos]    提示: $APP_BUNDLE 已就绪, 调用方应: terminate self -> replaceItemAt -> reopen"
fi
