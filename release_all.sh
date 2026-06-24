#!/bin/bash
# Token Monitor 统一发布脚本 (Mac + Windows)
#
# 用法: bash release_all.sh
#
# 流程:
#   1. 构建 Mac .app + DMG (build_macos.sh + build_dmg.sh)
#   2. 上传 DMG 到 GitCode Release
#   3. 如在 Windows 上: 构建 EXE + ZIP, 上传 ZIP
#   4. 更新 release notes
#
# GitCode Release 上传附件 (两步):
#   a) GET /releases/:tag/upload_url?file_name=xxx → 预签名 PUT 地址 + headers
#   b) PUT 文件到该地址
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/' || echo "1.3.44")
TAG="v${APP_VERSION}"
API_BASE="https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"

echo "============================================"
echo "  Token Monitor v${APP_VERSION} 统一发布"
echo "  Tag: ${TAG}"
echo "============================================"

# ─── GitCode 凭据 ───
GITCODE_TOKEN=$(printf 'protocol=https\nhost=gitcode.com\n\n' | git credential fill 2>/dev/null | awk -F= '$1=="password"{print $2}')
if [[ -z "$GITCODE_TOKEN" ]]; then
    echo "[release] ✘ 无法从 git credential 获取 gitcode.com token" >&2
    exit 1
fi

# 检查 Release 是否存在, 不存在则创建
RELEASE_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $GITCODE_TOKEN" \
    "$API_BASE/releases/tags/$TAG")

if [[ "$RELEASE_HTTP" != "200" ]]; then
    echo "[release] Release $TAG 不存在, 正在创建..."
    CREATE_RESP=$(curl -sS -X POST \
        -H "Authorization: Bearer $GITCODE_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"tag_name\":\"$TAG\",\"name\":\"Token Monitor $TAG\",\"body\":\"Token Monitor $TAG 发布\"}" \
        "$API_BASE/releases")
    echo "[release] Release 创建完成"
fi

# ─── 上传函数 ───
upload_asset() {
    local file_path="$1"
    local file_name="$2"

    echo "[release] 上传: $file_name"

    # 检查是否已有同名附件
    local existing
    existing=$(curl -sS \
        -H "Authorization: Bearer $GITCODE_TOKEN" \
        "$API_BASE/releases/tags/$TAG" | \
        python3 -c "
import json, sys
d = json.load(sys.stdin)
for a in d.get('assets', []):
    if a.get('name') == '$file_name':
        print(a.get('name'))
        break
" 2>/dev/null || true)

    if [[ -n "$existing" ]]; then
        echo "[release]   已有 '$file_name' 附件, 跳过"
        return 0
    fi

    # 获取预签名上传地址
    local upload_resp
    upload_resp=$(mktemp)
    local upload_http
    upload_http=$(curl -sS -o "$upload_resp" -w '%{http_code}' \
        -H "Authorization: Bearer $GITCODE_TOKEN" \
        -G --data-urlencode "file_name=$file_name" \
        "$API_BASE/releases/$TAG/upload_url")

    if [[ "$upload_http" != "200" ]]; then
        echo "[release]   ✘ 获取上传地址失败 (HTTP $upload_http)" >&2
        cat "$upload_resp"; echo
        rm -f "$upload_resp"
        return 1
    fi

    # PUT 上传
    local upload_code
    upload_code=$(python3 - "$upload_resp" "$file_path" <<'PYEOF'
import json, subprocess, sys

resp_file, file_path = sys.argv[1], sys.argv[2]
info = json.load(open(resp_file))
url = info["url"]
headers = info.get("headers", {})

cmd = ["curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "-X", "PUT"]
for k, v in headers.items():
    cmd += ["-H", f"{k}: {v}"]
cmd += ["--data-binary", f"@{file_path}", url]

result = subprocess.run(cmd, capture_output=True, text=True)
print(result.stdout.strip())
if result.returncode != 0:
    print(f"curl error: {result.stderr}", file=sys.stderr)
    sys.exit(1)
PYEOF
    )
    rm -f "$upload_resp"

    if [[ "$upload_code" != "200" ]]; then
        echo "[release]   ✘ 上传失败 (HTTP $upload_code)" >&2
        return 1
    fi
    echo "[release]   ✔ 上传成功"
    return 0
}

# ─── 1. Mac DMG ───
echo ""
echo "=== [1/2] Mac DMG ==="
if [[ "$(uname)" == "Darwin" ]]; then
    bash build_macos.sh
    chmod +x build_dmg.sh
    bash build_dmg.sh

    DMG_PATH="$SOURCE_ROOT/build/Token Monitor-${APP_VERSION}.dmg"
    if [[ -f "$DMG_PATH" ]]; then
        upload_asset "$DMG_PATH" "Token Monitor.dmg"
    else
        echo "[release] ✘ DMG 没生成, 跳过 Mac 上传" >&2
    fi
else
    echo "[release] 非 macOS, 跳过 DMG 构建"
fi

# ─── 2. Windows ZIP ───
echo ""
echo "=== [2/2] Windows ZIP ==="
WIN_PLATFORM=false
if [[ "$(uname)" == *"MINGW"* ]] || [[ "$(uname)" == *"MSYS"* ]] || [[ "$(uname)" == "CYGWIN_NT"* ]]; then
    WIN_PLATFORM=true
fi

if $WIN_PLATFORM; then
    bash build_windows.sh
    ZIP_PATH="$SOURCE_ROOT/build/TokenMonitor-${APP_VERSION}-win.zip"
    if [[ -f "$ZIP_PATH" ]]; then
        upload_asset "$ZIP_PATH" "TokenMonitor-win.zip"
    else
        echo "[release] ✘ Windows ZIP 没生成, 跳过" >&2
    fi
else
    echo "[release] 非 Windows, 跳过 EXE 构建"
    echo "  → 请在 Windows 机器上运行 build_windows.sh 并上传 ZIP"
fi

# ─── 验证 ───
echo ""
echo "=== 验证 Release assets ==="
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
    print(f'  {name} ({typ})')
    print(f'    → {url}')
"

echo ""
echo "============================================"
echo "  ✔ 发布完成: v${APP_VERSION}"
echo "============================================"
