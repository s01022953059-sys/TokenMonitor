#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HELPER="$ROOT_DIR/update_helper.sh"
TMP_DIR="$(mktemp -d /tmp/token-monitor-update-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

make_app() {
    local path="$1"
    local marker="$2"
    mkdir -p "$path/Contents/MacOS"
    printf '%s\n' "$marker" > "$path/Contents/version-marker"
}

assert_marker() {
    local path="$1"
    local expected="$2"
    test "$(cat "$path/Contents/version-marker")" = "$expected"
}

if grep -Eq 'with administrator privileges|(^|[[:space:]])sudo([[:space:]]|$)' "$HELPER"; then
    echo "[update-helper-test] 更新脚本仍包含管理员提权" >&2
    exit 1
fi

echo "[update-helper-test] 可写目录原地更新"
WRITABLE_STAGED="$TMP_DIR/writable-staged/Token Monitor.app"
WRITABLE_TARGET="$TMP_DIR/writable-target/Token Monitor.app"
make_app "$WRITABLE_STAGED" "new-writable"
make_app "$WRITABLE_TARGET" "old-writable"
TOKEN_MONITOR_UPDATE_TEST_MODE=1 \
    bash "$HELPER" "$WRITABLE_STAGED" "$WRITABLE_TARGET" "com.baggio.tokenmonitor"
assert_marker "$WRITABLE_TARGET" "new-writable"

echo "[update-helper-test] 不可写目录迁移到用户 Applications"
LOCKED_STAGED="$TMP_DIR/locked-staged/Token Monitor.app"
LOCKED_TARGET="$TMP_DIR/system-apps/Token Monitor.app"
USER_APPS="$TMP_DIR/user-apps"
make_app "$LOCKED_STAGED" "new-migrated"
make_app "$LOCKED_TARGET" "old-system-copy"
TOKEN_MONITOR_UPDATE_TEST_MODE=1 \
TOKEN_MONITOR_FORCE_TARGET_UNWRITABLE=1 \
TOKEN_MONITOR_USER_APPLICATIONS_DIR="$USER_APPS" \
    bash "$HELPER" "$LOCKED_STAGED" "$LOCKED_TARGET" "com.baggio.tokenmonitor"
assert_marker "$USER_APPS/Token Monitor.app" "new-migrated"
assert_marker "$LOCKED_TARGET" "old-system-copy"

echo "[update-helper-test] 两种更新路径均通过"
