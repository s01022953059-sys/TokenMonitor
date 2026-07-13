#!/bin/bash
# 第三层：只保留一条关键用户路径，避免 E2E 变慢、变脆。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PWCLI="$HOME/.codex/skills/playwright/scripts/playwright_cli.sh"
EDGE="/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"

if [[ "$(uname)" != "Darwin" ]]; then
    echo "[e2e] 非 macOS，跳过桌面 WebView 路径"
    exit 0
fi
command -v npx >/dev/null
test -x "$PWCLI"
test -x "$EDGE"

TMP_DIR=$(mktemp -d /tmp/token-monitor-e2e.XXXXXX)
PORT=$(python3 - <<'PY'
import socket
s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()
PY
)
SERVER_PID=""
cleanup() {
    "$PWCLI" close >/dev/null 2>&1 || true
    [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
    [[ -n "$SERVER_PID" ]] && wait "$SERVER_PID" 2>/dev/null || true
    rm -rf "$TMP_DIR" "$ROOT/.playwright-cli"
}
trap cleanup EXIT

# E2E 使用完整且确定的年度快照，验证前端会渲染到当天，而不是只验证空状态。
python3 - "$TMP_DIR/heatmap.json" <<'PY'
import datetime as dt
import json
import sys
import time

today = dt.date.today()
rows = []
for offset in range(365):
    current = today - dt.timedelta(days=364 - offset)
    rows.append({
        "date": current.isoformat(),
        "label": current.strftime("%m-%d"),
        "weekday": current.weekday(),
        "month": current.month,
        "tokens": 1 if offset in (0, 364) else 0,
    })
with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"saved_at": time.time(), "data": {"days": rows}}, stream)
PY

HOME="$TMP_DIR/home" TOKEN_MONITOR_LOCK_FILE="$TMP_DIR/server.lock" TOKEN_MONITOR_HEATMAP_CACHE_FILE="$TMP_DIR/heatmap.json" TOKEN_MONITOR_DISABLE_COMMUNITY_REPORT=1 \
    python3 "$ROOT/server.py" --port "$PORT" --update-feed-url "" >"$TMP_DIR/server.log" 2>&1 &
SERVER_PID=$!
for _ in {1..80}; do
    curl -fsS "http://127.0.0.1:$PORT/api/app-info" >/dev/null 2>&1 && break
    sleep 0.1
done
curl -fsS "http://127.0.0.1:$PORT/api/app-info" >/dev/null

EXPECTED_30=$(python3 - <<'PY'
import datetime
print((datetime.date.today() - datetime.timedelta(days=29)).isoformat())
PY
)
EXPECTED_365=$(python3 - <<'PY'
import datetime
print((datetime.date.today() - datetime.timedelta(days=364)).isoformat())
PY
)
EXPECTED_TODAY=$(python3 - <<'PY'
import datetime
print(datetime.date.today().isoformat())
PY
)

"$PWCLI" open "http://127.0.0.1:$PORT" --browser msedge --headed >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
HEATMAP_REF=$(printf '%s\n' "$SNAPSHOT" | sed -nE 's/.*button "活动热力图".*\[ref=([^]]+)\].*/\1/p' | head -1)
test -n "$HEATMAP_REF"
"$PWCLI" click "$HEATMAP_REF" >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
printf '%s\n' "$SNAPSHOT" | grep -q "$EXPECTED_30 至"
printf '%s\n' "$SNAPSHOT" | grep -q "$EXPECTED_TODAY ("

YEAR_REF=$(printf '%s\n' "$SNAPSHOT" | sed -nE 's/.*button "近一年".*\[ref=([^]]+)\].*/\1/p' | head -1)
test -n "$YEAR_REF"
"$PWCLI" click "$YEAR_REF" >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
printf '%s\n' "$SNAPSHOT" | grep -q "近一年.*\[active\]"
printf '%s\n' "$SNAPSHOT" | grep -q "$EXPECTED_365 至"
printf '%s\n' "$SNAPSHOT" | grep -q "$EXPECTED_TODAY ("

# 每日调用详情是 macOS 曾出现长时间卡住的路径：点击当天格子后不能一直停在加载态。
"$PWCLI" eval "() => document.querySelector('.heatmap-cell-day[data-date=\"$EXPECTED_TODAY\"]').click()" >/dev/null
for _ in {1..20}; do
    SNAPSHOT=$("$PWCLI" snapshot)
    if ! printf '%s\n' "$SNAPSHOT" | grep -q "加载中...\|正在整理当天明细"; then
        break
    fi
    sleep 0.2
done
printf '%s\n' "$SNAPSHOT" | grep -q "$EXPECTED_TODAY 调用详情"
! printf '%s\n' "$SNAPSHOT" | grep -q "加载中...\|正在整理当天明细"

# About 必须展示当前版本的简短更新摘要，不能只依赖发布时人工目测。
"$PWCLI" eval "() => document.getElementById('heatmapDetailModal').classList.remove('active')" >/dev/null
"$PWCLI" eval "() => document.getElementById('aboutOpenBtn').click()" >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
printf '%s\n' "$SNAPSHOT" | grep -q "本次更新"
printf '%s\n' "$SNAPSHOT" | grep -q "社区用量改为每 5 分钟静默同步"

echo "[e2e] PASS: 首页 -> 热力图 -> 近一年范围 -> 当日调用详情 -> About 更新摘要"
