#!/bin/bash
# Token Monitor dmg 打包脚本
#
# 用法: 在源码根目录下执行 bash build_dmg.sh [--output PATH]
# 依赖: macOS 自带 hdiutil (不需要 Xcode CLT)
#
# 产物: Token Monitor.dmg (含 .app + /Applications symlink, 双击拖装)
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="Token Monitor"
BUILD_OUTPUT="$SOURCE_ROOT/build/$APP_NAME.app"
DMG_NAME="${APP_NAME}.dmg"
DMG_OUTPUT="$SOURCE_ROOT/build/$DMG_NAME"

# 解析参数
while [[ $# -gt 0 ]]; do
    case "$1" in
        --output)
            DMG_OUTPUT="$2"
            shift 2
            ;;
        *)
            echo "[build_dmg] 未知参数: $1" >&2
            exit 1
            ;;
    esac
done

# 检查 .app 是否已经 build 出来
if [[ ! -d "$BUILD_OUTPUT" ]]; then
    echo "[build_dmg] ✘ 找不到 $BUILD_OUTPUT, 请先跑 bash build_macos.sh" >&2
    exit 1
fi

# 检查 macOS (hdiutil 只在 macOS 上有)
if [[ "$(uname)" != "Darwin" ]]; then
    echo "[build_dmg] ✘ 此脚本只能在 macOS 上跑 (hdiutil 不存在)" >&2
    exit 1
fi

# 检查 VERSION
if [[ ! -f "$BUILD_OUTPUT/Contents/Info.plist" ]]; then
    echo "[build_dmg] ✘ $BUILD_OUTPUT/Contents/Info.plist 缺失" >&2
    exit 1
fi
APP_VERSION=$(plutil -extract CFBundleShortVersionString raw -o - "$BUILD_OUTPUT/Contents/Info.plist")
DMG_NAME="Token Monitor-${APP_VERSION}.dmg"
echo "[build_dmg] app version: $APP_VERSION"
echo "[build_dmg] output: $SOURCE_ROOT/build/$DMG_NAME"

# 创建一个临时 dmg 暂存目录, 拷 .app + 创建 /Applications symlink
# 用户双击 dmg 后, 会看到一个 Finder 窗口: Token Monitor.app + Applications
# 的 alias, 拖 .app 到 Applications alias 就装好。
TMPDIR=$(mktemp -d -t tm-dmg.XXXXXX)
trap "rm -rf '$TMPDIR'" EXIT
DMG_STAGE="$TMPDIR/dmg-stage"
mkdir -p "$DMG_STAGE"
cp -R "$BUILD_OUTPUT" "$DMG_STAGE/$APP_NAME.app"
ln -s /Applications "$DMG_STAGE/Applications"
echo "[build_dmg] 暂存目录: $DMG_STAGE (含 .app + /Applications symlink)"

# 用 hdiutil 打 dmg
# -ov: overwrite existing
# -fs HFS+: 兼容老 macOS (APFS 不被 10.12 之前读)
# -volname: dmg mount 时显示的卷名
# -fsargs: 给新 macOS 加 -nocrossdev 优化
# -imagekey zlib-level=9: 压缩级别 (9 = 最小 dmg, 速度慢一些)
echo "[build_dmg] 正在打 dmg..."
hdiutil create \
    -ov \
    -format UDZO \
    -fs HFS+ \
    -volname "$APP_NAME $APP_VERSION" \
    -srcfolder "$DMG_STAGE" \
    -imagekey zlib-level=9 \
    "$SOURCE_ROOT/build/$DMG_NAME"

if [[ ! -f "$SOURCE_ROOT/build/$DMG_NAME" ]]; then
    echo "[build_dmg] ✘ dmg 生成失败" >&2
    exit 1
fi

DMG_SIZE=$(du -h "$SOURCE_ROOT/build/$DMG_NAME" | cut -f1)
echo "[build_dmg] ✔ dmg 生成完成: build/$DMG_NAME ($DMG_SIZE)"
echo ""
echo "使用方法:"
echo "  1. Finder 双击 $SOURCE_ROOT/build/$DMG_NAME"
echo "  2. 拖 Token Monitor.app 到 Applications alias"
echo "  3. 弹出 dmg, app 装好"
echo ""
echo "或命令行:"
echo "  open $SOURCE_ROOT/build/$DMG_NAME"