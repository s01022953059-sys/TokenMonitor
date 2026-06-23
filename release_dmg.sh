#!/bin/bash
# Token Monitor dmg 发布脚本
#
# 用法: 在源码根目录下执行 bash release_dmg.sh
# 依赖: macOS + hdiutil (build_dmg.sh) + git
#
# 这个脚本做 4 件事:
#   1. 跑 build_macos.sh 编译 .app
#   2. 跑 build_dmg.sh 打 dmg
#   3. 把 dmg 拷到 dist/ 目录 (只保留最新版, 旧版删除)
#   4. git commit + push, 让 gitcode raw URL 能下载 dmg
#
# 为什么 commit 进 git: gitcode API 不支持上传 release asset,
# 只能靠 source archive。dmg 是二进制 build 产物, 不在 source 里,
# 只能 commit 进 git 让 raw URL 能下载。
#
# 代价: git 仓库会累积 dmg 二进制 (每个 ~540 KB), 但 dist/ 工作树
# 只保留最新一个, 旧版在 git history 里。
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

# 检查 macOS
if [[ "$(uname)" != "Darwin" ]]; then
    echo "[release_dmg] ✘ 此脚本只能在 macOS 上跑 (hdiutil 不存在)" >&2
    exit 1
fi

# 读当前版本号
APP_VERSION=$(plutil -extract CFBundleShortVersionString raw -o - "$SOURCE_ROOT/Info.plist")
DMG_NAME="Token Monitor-${APP_VERSION}.dmg"
echo "[release_dmg] 版本: $APP_VERSION"
echo "[release_dmg] dmg: $DMG_NAME"

# 1. build .app
echo ""
echo "[release_dmg] [1/4] 编译 .app"
bash build_macos.sh

# 2. build dmg
echo ""
echo "[release_dmg] [2/4] 打 dmg"
chmod +x build_dmg.sh
bash build_dmg.sh

DMG_PATH="$SOURCE_ROOT/build/$DMG_NAME"
if [[ ! -f "$DMG_PATH" ]]; then
    echo "[release_dmg] ✘ dmg 没生成: $DMG_PATH" >&2
    exit 1
fi

# 3. 拷到 dist/, 删旧 dmg (只留最新)
echo ""
echo "[release_dmg] [3/4] 拷到 dist/ (删旧 dmg)"
mkdir -p "$SOURCE_ROOT/dist"
# 删 dist/ 下所有旧 dmg
find "$SOURCE_ROOT/dist" -name "Token Monitor-*.dmg" -delete 2>/dev/null || true
cp "$DMG_PATH" "$SOURCE_ROOT/dist/$DMG_NAME"
ls -la "$SOURCE_ROOT/dist/"

# 4. git commit + push
echo ""
echo "[release_dmg] [4/4] git commit + push"
git add "dist/$DMG_NAME"
# 也 add 删除的旧 dmg
git add -A dist/ 2>/dev/null || true

# 检查有没有改动
if git diff --cached --quiet; then
    echo "[release_dmg] 没有改动, 跳过 commit"
else
    git commit -m "dist: 发布 $DMG_NAME (v$APP_VERSION)

dmg 二进制 commit 进 git, 因为 gitcode API 不支持上传 release asset。
raw URL: https://raw.gitcode.com/baggiopeng/TokenMonitor/main/dist/$DMG_NAME"
    # push 需要 credential, 用 remote URL 里的 token
    # 如果 remote 没配 token, 提示用户手动 push
    if git remote get-url origin | grep -q "baggiopeng:"; then
        git push origin main 2>&1 | tail -3
    else
        echo "[release_dmg] [!] remote 没配 token, 请手动 push:"
        echo "  git push origin main"
    fi
fi

echo ""
echo "[release_dmg] ✔ 完成"
echo ""
echo "下载地址 (raw URL):"
echo "  https://raw.gitcode.com/baggiopeng/TokenMonitor/main/dist/$DMG_NAME"
echo ""
echo "或 gitcode 网页:"
echo "  https://gitcode.com/baggiopeng/TokenMonitor/blob/main/dist/$DMG_NAME"
