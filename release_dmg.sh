#!/bin/bash
# Token Monitor DMG 发布脚本
#
# 用法: bash release_dmg.sh
# 依赖: macOS + hdiutil + git credential (gitcode.com)
#
# 流程:
#   1. build_macos.sh 编译 .app
#   2. build_dmg.sh 打 dmg
#   3. 通过 GitCode API 上传 DMG 到对应 tag 的 Release 附件
#
# GitCode 上传附件是两步:
#   a) GET /releases/:tag/upload_url?file_name=xxx  → 拿到预签名 PUT 地址 + headers
#   b) PUT DMG 到那个地址
#
# 凭据从 git credential 读取 (host=gitcode.com), 不硬编码 token。
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

if [[ "$(uname)" != "Darwin" ]]; then
    echo "[release_dmg] ✘ 此脚本只能在 macOS 上跑 (hdiutil 不存在)" >&2
    exit 1
fi

APP_VERSION=$(plutil -extract CFBundleShortVersionString raw -o - "$SOURCE_ROOT/Info.plist")
DMG_NAME="Token Monitor-${APP_VERSION}.dmg"
TAG="v${APP_VERSION}"
API_BASE="https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"

echo "[release_dmg] 版本: $APP_VERSION"
echo "[release_dmg] tag:   $TAG"

# ---- 1. 编译 .app ----
echo ""
echo "[release_dmg] [1/3] 编译 .app"
bash build_macos.sh

# ---- 2. 打 DMG ----
echo ""
echo "[release_dmg] [2/3] 打 dmg"
chmod +x build_dmg.sh
bash build_dmg.sh

DMG_PATH="$SOURCE_ROOT/build/$DMG_NAME"
if [[ ! -f "$DMG_PATH" ]]; then
    echo "[release_dmg] ✘ dmg 没生成: $DMG_PATH" >&2
    exit 1
fi

DMG_SIZE=$(du -h "$DMG_PATH" | cut -f1)
echo "[release_dmg] dmg: $DMG_PATH ($DMG_SIZE)"

# ---- 3. 上传到 GitCode Release ----
echo ""
echo "[release_dmg] [3/3] 上传 DMG 到 GitCode Release ($TAG)"

# 从 git credential 拿 token (不打印)
GITCODE_TOKEN=$(printf 'protocol=https\nhost=gitcode.com\n\n' | git credential fill 2>/dev/null | awk -F= '$1=="password"{print $2}')
if [[ -z "$GITCODE_TOKEN" ]]; then
    echo "[release_dmg] ✘ 无法从 git credential 获取 gitcode.com 的 token" >&2
    echo "  请确保 git push 到 gitcode 时能正常认证 (git credential fill 有 password 字段)" >&2
    exit 1
fi

# 检查 Release 是否存在
RELEASE_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $GITCODE_TOKEN" \
    "$API_BASE/releases/tags/$TAG")

if [[ "$RELEASE_HTTP" != "200" ]]; then
    echo "[release_dmg] ✘ Release $TAG 不存在 (HTTP $RELEASE_HTTP), 请先创建 Release" >&2
    exit 1
fi

# 检查是否已有同名附件 (同名会导致 GitCode 拒绝上传)
EXISTING=$(curl -sS \
    -H "Authorization: Bearer $GITCODE_TOKEN" \
    "$API_BASE/releases/tags/$TAG" | \
    python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('assets', []):
    if a.get('name') == 'Token Monitor.dmg':
        print(a.get('name'))
        break
" 2>/dev/null || true)

if [[ -n "$EXISTING" ]]; then
    echo "[release_dmg] Release 里已有 'Token Monitor.dmg' 附件, 跳过上传"
    echo "  如需重新上传, 请先在 GitCode 网页删除旧附件"
else
    # 3a. 获取预签名上传地址
    UPLOAD_RESP=$(mktemp)
    UPLOAD_HTTP=$(curl -sS -o "$UPLOAD_RESP" -w '%{http_code}' \
        -H "Authorization: Bearer $GITCODE_TOKEN" \
        -G --data-urlencode "file_name=Token Monitor.dmg" \
        "$API_BASE/releases/$TAG/upload_url")

    if [[ "$UPLOAD_HTTP" != "200" ]]; then
        echo "[release_dmg] ✘ 获取上传地址失败 (HTTP $UPLOAD_HTTP)" >&2
        cat "$UPLOAD_RESP"; echo
        rm -f "$UPLOAD_RESP"
        exit 1
    fi

    # 3b. 用 Python 解析 JSON 并构造 curl 命令上传
    UPLOAD_CODE=$(python3 - "$UPLOAD_RESP" "$DMG_PATH" <<'PYEOF'
import json, subprocess, sys, shlex

resp_file, dmg_path = sys.argv[1], sys.argv[2]
info = json.load(open(resp_file))
url = info["url"]
headers = info.get("headers", {})

cmd = ["curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "-X", "PUT"]
for k, v in headers.items():
    cmd += ["-H", f"{k}: {v}"]
cmd += ["--data-binary", f"@{dmg_path}", url]

result = subprocess.run(cmd, capture_output=True, text=True)
print(result.stdout.strip())
if result.returncode != 0:
    print(f"curl error: {result.stderr}", file=sys.stderr)
    sys.exit(1)
PYEOF
    )
    rm -f "$UPLOAD_RESP"

    if [[ "$UPLOAD_CODE" != "200" ]]; then
        echo "[release_dmg] ✘ 上传 DMG 失败 (HTTP $UPLOAD_CODE)" >&2
        exit 1
    fi
    echo "[release_dmg] ✔ DMG 上传成功"
fi

# 验证
echo ""
echo "[release_dmg] 验证 Release assets:"
curl -sS \
    -H "Authorization: Bearer $GITCODE_TOKEN" \
    "$API_BASE/releases/tags/$TAG" | \
    python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('assets', []):
    name = a.get('name', '')
    typ = a.get('type', '')
    url = a.get('browser_download_url', '')
    marker = '★' if 'dmg' in name.lower() else '-'
    print(f'  {marker} {name} ({typ}) -> {url[:80]}')
"

echo ""
echo "[release_dmg] ✔ 完成"
echo ""
echo "DMG 下载地址:"
echo "  $API_BASE/releases/download/$TAG/Token Monitor.dmg"
