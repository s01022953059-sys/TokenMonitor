#!/bin/bash
# 发布前基础验证。只测试和构建，不修改版本号、不创建 tag、不上传 Release。
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
GO_VERSION=$(sed -n 's/^var appVersion = "\([^"]*\)"/\1/p' go_build/main.go | head -1)
if [[ "$APP_VERSION" != "$GO_VERSION" ]]; then
    echo "[verify] 版本号不同步: Info.plist=$APP_VERSION Go=$GO_VERSION" >&2
    exit 1
fi

echo "[verify] Python 单元测试"
python3 -m unittest discover -s tests -p 'test_*.py' -v

if [[ "$(uname)" == "Darwin" ]]; then
    echo "[verify] macOS 无密码更新路径"
    bash tests/test_update_helper.sh
fi

echo "[verify] Go 单元测试"
(cd go_build && go test ./...)

echo "[verify] 社区中继单元测试"
(cd community_relay && go test ./...)

echo "[verify] 社区中继公网健康检查"
curl -fsS --max-time 15 "https://new.taqi.cc/token-monitor-community/health" | \
    python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("ok") is True'

echo "[verify] 前端语法与双端资源同步"
node - <<'NODE'
const fs = require('fs');
for (const file of ['index.html', 'go_build/static/index.html']) {
  const html = fs.readFileSync(file, 'utf8');
  const selected = html.match(/<button class="tab-btn active" data-days="(30|90|180|365)">近/);
  const initial = html.match(/let _heatmapDays = (30|90|180|365);/);
  if (!selected || !initial || selected[1] !== initial[1]) {
    throw new Error(`${file}: 热力图默认 Tab 与请求范围不一致`);
  }
  const scripts = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi)];
  scripts.forEach((match) => new Function(match[1]));
}
NODE
cmp -s index.html go_build/static/index.html

TMP_DIR=$(mktemp -d /tmp/token-monitor-verify.XXXXXX)
SERVER_PID=""
cleanup() {
    if [[ -n "$SERVER_PID" ]]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "[verify] 本地 API 冒烟"
PORT=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)
TOKEN_MONITOR_LOCK_FILE="$TMP_DIR/server.lock" TOKEN_MONITOR_HEATMAP_CACHE_FILE="$TMP_DIR/heatmap.json" TOKEN_MONITOR_LOCAL_API_TOKEN="verify-local-token" python3 server.py \
    --port "$PORT" \
    --update-feed-url "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest" \
    >"$TMP_DIR/server.log" 2>&1 &
SERVER_PID=$!
for _ in {1..30}; do
    curl -fsS "http://127.0.0.1:$PORT/api/app-info" >/dev/null 2>&1 && break
    sleep 0.2
done
python3 - "$PORT" <<'PY'
import json
import sys
import urllib.error
import urllib.request
import time

port = int(sys.argv[1])
def get(path):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=15) as response:
        return json.load(response)

usage = get("/api/usage")
assert usage["summary"]["total_tokens"] > 0, "今日 Token 为 0"
heatmap = get("/api/heatmap?days=365")
assert len(heatmap["days"]) == 365, "近一年热力图不是 365 天"
started = time.monotonic()
heatmap_30 = get("/api/heatmap?days=30")
elapsed = time.monotonic() - started
assert len(heatmap_30["days"]) == 30, "近 30 天热力图不是 30 天"
assert elapsed < 0.5, f"热力图缓存命中耗时过长: {elapsed:.3f}s"
sessions = get("/api/sessions?days=7&page=1&page_size=20")
assert sessions["page"] == 1 and sessions["page_size"] == 20, "会话分页异常"
update = get("/api/check-update")
assert update["ok"] and update["download_url"], "更新检查或安装包选择失败"
assert not update["download_url"].startswith("https://api.gitcode.com/"), "附件仍使用不可下载的 API 域名"
community = get("/api/community")
assert community.get("data_status") != "load_failed", community.get("error")
assert community.get("can_report") is True, "社区中继未启用"

request = urllib.request.Request(
    f"http://127.0.0.1:{port}/api/community/profile",
    data=json.dumps({}).encode(), method="POST", headers={"Content-Type": "application/json"},
)
try:
    urllib.request.urlopen(request, timeout=15)
    raise AssertionError("昵称接口接受了无效请求")
except urllib.error.HTTPError as exc:
    assert exc.code == 400, f"昵称接口错误状态异常: {exc.code}"

cross_origin = urllib.request.Request(
    f"http://127.0.0.1:{port}/api/community/profile",
    data=json.dumps({"display_name": "安全昵称"}).encode(), method="POST",
    headers={"Content-Type": "application/json", "Origin": "https://example.com"},
)
try:
    urllib.request.urlopen(cross_origin, timeout=15)
    raise AssertionError("昵称接口接受了跨站请求")
except urllib.error.HTTPError as exc:
    assert exc.code == 403, f"跨站防护状态异常: {exc.code}"

file_origin_without_token = urllib.request.Request(
    f"http://127.0.0.1:{port}/api/community/profile",
    data=json.dumps({}).encode(), method="POST",
    headers={"Content-Type": "application/json", "Origin": "null"},
)
try:
    urllib.request.urlopen(file_origin_without_token, timeout=15)
    raise AssertionError("macOS file 来源未携带凭据仍被接受")
except urllib.error.HTTPError as exc:
    assert exc.code == 403, f"macOS file 无凭据状态异常: {exc.code}"

authenticated_file_origin = urllib.request.Request(
    f"http://127.0.0.1:{port}/api/community/profile",
    data=json.dumps({}).encode(), method="POST",
    headers={
        "Content-Type": "application/json",
        "Origin": "null",
        "X-Token-Monitor-Client": "verify-local-token",
    },
)
try:
    urllib.request.urlopen(authenticated_file_origin, timeout=15)
    raise AssertionError("昵称接口接受了无效请求")
except urllib.error.HTTPError as exc:
    assert exc.code == 400, f"macOS file 合法凭据未通过来源校验: {exc.code}"

authenticated_file_url_origin = urllib.request.Request(
    f"http://127.0.0.1:{port}/api/community/profile",
    data=json.dumps({}).encode(), method="POST",
    headers={
        "Content-Type": "application/json",
        "Origin": "file://",
        "X-Token-Monitor-Client": "verify-local-token",
    },
)
try:
    urllib.request.urlopen(authenticated_file_url_origin, timeout=15)
    raise AssertionError("昵称接口接受了无效请求")
except urllib.error.HTTPError as exc:
    assert exc.code == 400, f"macOS file:// 合法凭据未通过来源校验: {exc.code}"
print(f"[verify] API OK: tokens={usage['summary']['total_tokens']}, heatmap=365, warm={elapsed:.3f}s, sessions={len(sessions['sessions'])}")
PY
kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""

echo "[verify] Windows 正式安装程序"
bash build_windows.sh
file build/TokenMonitor-Setup.exe | grep -q 'PE32+ executable (GUI)'
file build/TokenMonitor.exe | grep -q 'PE32+ executable (GUI)'
cmp -s build/TokenMonitor-Setup.exe build/TokenMonitor.exe
test "$(stat -f%z build/TokenMonitor-Setup.exe 2>/dev/null || stat -c%s build/TokenMonitor-Setup.exe)" -gt 10000000

if [[ "$(uname)" == "Darwin" ]]; then
    echo "[verify] macOS App 构建"
    bash build_macos.sh "$TMP_DIR/mac-build"
    test -x "$TMP_DIR/mac-build/Token Monitor.app/Contents/MacOS/TokenMonitor"
fi

echo "[verify] 全部基础验证通过 (v$APP_VERSION)"
