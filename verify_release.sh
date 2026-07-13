#!/bin/bash
# 发布前质量门禁：按 Unit -> API -> E2E -> 构建顺序执行，任何层失败立即终止。
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SOURCE_ROOT"

APP_VERSION=$(grep -o 'CFBundleShortVersionString' Info.plist -A1 | grep '<string>' | head -1 | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
GO_VERSION=$(sed -n 's/^var appVersion = "\([^"]*\)"/\1/p' go_build/main.go | head -1)
if [[ "$APP_VERSION" != "$GO_VERSION" ]]; then
    echo "[verify] 版本号不同步: Info.plist=$APP_VERSION Go=$GO_VERSION" >&2
    exit 1
fi

echo "[verify] Layer 1/3: Unit"
bash tests/run_unit_tests.sh

echo "[verify] Layer 2/3: API contract (Python + Go)"
python3 tests/api_contract.py --backend all

echo "[verify] Layer 3/3: E2E"
bash tests/e2e_ui.sh

echo "[verify] 构建 Windows 正式安装程序"
bash build_windows.sh
file build/TokenMonitor-Setup.exe | grep -q 'PE32+ executable (GUI)'
file build/TokenMonitor.exe | grep -q 'PE32+ executable (GUI)'
cmp -s build/TokenMonitor-Setup.exe build/TokenMonitor.exe
test "$(stat -f%z build/TokenMonitor-Setup.exe 2>/dev/null || stat -c%s build/TokenMonitor-Setup.exe)" -gt 10000000

if [[ "$(uname)" == "Darwin" ]]; then
    TMP_DIR=$(mktemp -d /tmp/token-monitor-verify.XXXXXX)
    trap 'rm -rf "$TMP_DIR"' EXIT
    echo "[verify] 构建 macOS App"
    bash build_macos.sh "$TMP_DIR/mac-build"
    test -x "$TMP_DIR/mac-build/Token Monitor.app/Contents/MacOS/TokenMonitor"
fi

echo "[verify] PASS: 全部质量门禁通过 (v$APP_VERSION)"
