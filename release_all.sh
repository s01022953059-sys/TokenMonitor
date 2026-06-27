#!/bin/bash
# Token Monitor 统一发布脚本 (Mac + Windows)
#
# 用法: bash release_all.sh
#
# 流程:
#   0. 清理 build/ 目录, 防止旧产物混入
#   1. 构建 Mac .app + DMG, 上传到 GitCode Release
#   2. 交叉编译 Windows EXE + ZIP, 上传到同一 Release
#   3. 清理 build/ 目录
#
# GitCode Release 上传附件 (两步):
#   a) GET /releases/:tag/upload_url?file_name=xxx → 预签名 PUT 地址 + headers
#   b) PUT 文件到该地址
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
TAG="v${APP_VERSION}"
API_BASE="https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor"

echo "============================================"
echo "  Token Monitor v${APP_VERSION} 统一发布"
echo "  Tag: ${TAG}"
echo "============================================"

# ─── 0. 清理 build/ 目录 ───
echo ""
echo "=== [0/3] 清理旧构建产物 ==="
rm -rf "$SOURCE_ROOT/build/"
mkdir -p "$SOURCE_ROOT/build/"
echo "[release] build/ 已清空"

# ─── GitCode 凭据 ───
GITCODE_TOKEN=$(printf 'protocol=https\nhost=gitcode.com\n\n' | git credential fill 2>/dev/null | awk -F= '$1=="password"{print $2}')
if [[ -z "$GITCODE_TOKEN" ]]; then
    echo "[release] ✘ 无法从 git credential 获取 gitcode.com token" >&2
    exit 1
fi

# ─── 确保 git tag $TAG 已存在且已 push ───
# v1.3.72 事故: 没打 tag 就创建 release, GitCode 拿当时的 HEAD (v1.3.71 commit),
# 导致 DMG/源码 zip 都是 v1.3.71 旧代码, 客户端"升级"装上的还是旧版。
# 修复: 在创建 release 前先 git tag + push, 并在 payload 显式传 target_commitish。
COMMIT_SHA=$(git rev-parse HEAD)
if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then
    echo "[release] 本地没有 $TAG tag, 正在创建并 push..."
    if ! git tag "$TAG"; then
        echo "[release] ✘ git tag $TAG 失败" >&2
        exit 1
    fi
    if ! git push origin "$TAG" 2>&1 | tail -5; then
        echo "[release] ✘ git push origin $TAG 失败 (tag 已本地创建, 需手动 push)" >&2
        exit 1
    fi
    COMMIT_SHA=$(git rev-parse "refs/tags/$TAG")
    echo "[release] tag $TAG 已 push, 指向 $COMMIT_SHA"
else
    COMMIT_SHA=$(git rev-parse "refs/tags/$TAG")
    # tag 存在但 HEAD 跟它不一致, 警告 (不阻断, 让发版继续)
    HEAD_SHA=$(git rev-parse HEAD)
    if [[ "$COMMIT_SHA" != "$HEAD_SHA" ]]; then
        echo "[release] ⚠ HEAD ($HEAD_SHA) 与 tag $TAG ($COMMIT_SHA) 不一致, release 将用 tag 指向的 commit"
    fi
fi

# 检查 Release 是否存在, 不存在则创建
RELEASE_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $GITCODE_TOKEN" \
    "$API_BASE/releases/tags/$TAG")

if [[ "$RELEASE_HTTP" != "200" ]]; then
    echo "[release] Release $TAG 不存在, 正在创建 (target_commitish=refs/tags/$TAG)..."
    # 取最新 commit 的 subject 作为 body (避免 fallback 占位文案)
    BODY="$(git log -1 --format=%s $TAG 2>/dev/null || echo "$TAG - Mac + Windows 统一发布")"
    # 写响应到临时文件, 读 HTTP 状态码; 失败时 cat 响应体便于排查, exit 1 阻断后续
    # 重试 3 次: GitCode 的 tag 跟 release API 有最终一致性, 偶尔 POST 时 tag 还没同步,
    # 等几秒重试就好
    # 把 JSON payload 写到临时文件, 用 curl --data-binary @file 避免 bash 双引号解析问题
    CREATE_RESP=$(mktemp)
    CREATE_PAYLOAD=$(mktemp)
    python3 -c "import json; json.dump({'tag_name': '$TAG', 'name': 'Token Monitor $TAG', 'target_commitish': 'refs/tags/$TAG', 'body': '''$BODY'''}, open('$CREATE_PAYLOAD', 'w'))"
    CREATE_HTTP=""
    for attempt in 1 2 3; do
        CREATE_HTTP=$(curl -sS -o "$CREATE_RESP" -w '%{http_code}' \
            -H "Authorization: Bearer $GITCODE_TOKEN" \
            -H "Content-Type: application/json" \
            --data-binary "@$CREATE_PAYLOAD" \
            "$API_BASE/releases")
        if [[ "$CREATE_HTTP" == "201" || "$CREATE_HTTP" == "200" ]]; then
            break
        fi
        echo "[release]   POST 第 $attempt 次失败 (HTTP $CREATE_HTTP), 3 秒后重试..."
        sleep 3
    done
    if [[ "$CREATE_HTTP" != "201" && "$CREATE_HTTP" != "200" ]]; then
        echo "[release]   ✘ Release 创建失败 (HTTP $CREATE_HTTP, 已重试 3 次)" >&2
        echo "[release]   响应体:" >&2
        cat "$CREATE_RESP" >&2
        echo "" >&2
        rm -f "$CREATE_RESP" "$CREATE_PAYLOAD"
        exit 1
    fi
    rm -f "$CREATE_RESP" "$CREATE_PAYLOAD"
    echo "[release] Release 创建完成 (HTTP $CREATE_HTTP)"
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

    if [[ "$upload_code" == "200" ]] || [[ "$upload_code" == "203" ]]; then
        echo "[release]   ✔ 上传成功"
    else
        echo "[release]   ✘ 上传失败 (HTTP $upload_code)" >&2
        return 1
    fi
    return 0
}

# ─── 1. Mac DMG ───
echo ""
echo "=== [1/3] Mac DMG ==="
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

# ─── 2. Windows ZIP (Go 交叉编译, 任何平台都能跑) ───
echo ""
echo "=== [2/3] Windows ZIP ==="
bash build_windows.sh

ZIP_PATH="$SOURCE_ROOT/build/TokenMonitor-${APP_VERSION}-win.zip"
if [[ -f "$ZIP_PATH" ]]; then
    upload_asset "$ZIP_PATH" "TokenMonitor-win.zip"
else
    echo "[release] ✘ Windows ZIP 没生成" >&2
fi

# ─── 3. 清理 build/ 目录 ───
echo ""
echo "=== [3/3] 清理构建产物 ==="
rm -rf "$SOURCE_ROOT/build/"
echo "[release] build/ 已清空"

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
    if typ == 'attach':
        print(f'  {name}')
        print(f'    -> {url}')
"

echo ""
echo "============================================"
echo "  ✔ 发布完成: v${APP_VERSION}"
echo "============================================"
