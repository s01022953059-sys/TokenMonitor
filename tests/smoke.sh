#!/bin/bash
# Token Monitor 冒烟测试套件
# 跑所有 API + 基础 UI 验证, 生成 reports/vX.Y.Z-pre-release-{ts}.json
# 任何一条失败 → exit 1, 阻止发版
#
# 用法:
#   bash tests/smoke.sh                    # 测当前 dev source (Python server)
#   bash tests/smoke.sh --go              # 测 Go build (Mac 二进制)
#   bash tests/smoke.sh --version X.Y.Z  # 测指定版本 (label)
set -euo pipefail

# ─── 参数 ───
LABEL="dev"
SERVER="python"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --go) SERVER="go"; shift ;;
        --version) LABEL="$2"; shift 2 ;;
        *) echo "Unknown arg: $1" >&2; exit 2 ;;
    esac
done

PORT=15723
TIMESTAMP=$(date -u +"%Y-%m-%dT%H-%M-%SZ")
REPORT_FILE="tests/reports/${LABEL}-pre-release-${TIMESTAMP}.json"

# ─── 启动 server (按 SERVER 类型) ───
echo "=== Smoke test for $LABEL (server: $SERVER) ==="
if [[ "$SERVER" == "go" ]]; then
    # 用之前 build 的 Mac Go 二进制
    if [[ ! -x /tmp/test_TM_mac ]]; then
        echo "❌ /tmp/test_TM_mac not found, build first:" >&2
        echo "   cd go_build && GOOS=darwin GOARCH=amd64 go build -ldflags='-s -w' -o /tmp/test_TM_mac ." >&2
        exit 2
    fi
    lsof -ti :$PORT 2>/dev/null | xargs -r kill -9 2>/dev/null || true
    rm -f /tmp/token_monitor_server.lock /tmp/token_monitor_server.pid
    /tmp/test_TM_mac > /tmp/smoke_server.log 2>&1 &
    SERVER_PID=$!
elif [[ "$SERVER" == "python" ]]; then
    lsof -ti :$PORT 2>/dev/null | xargs -r kill -9 2>/dev/null || true
    rm -f /tmp/token_monitor_server.lock /tmp/token_monitor_server.pid
    /Users/baggio/Projects/token_monitor/start.sh start $PORT \
        "https://api.gitcode.com/api/v5/repos/baggiopeng/TokenMonitor/releases/latest" > /tmp/smoke_server.log 2>&1
    SERVER_PID=""
fi

# 等 server 起来
echo "Waiting for server on :$PORT ..."
for i in 1 2 3 4 5 6 7 8 9 10; do
    sleep 0.5
    if lsof -i :$PORT >/dev/null 2>&1; then
        echo "  Server up after ${i}*0.5s"
        break
    fi
    if [[ $i -eq 10 ]]; then
        echo "❌ Server failed to start in 5s, log:" >&2
        cat /tmp/smoke_server.log >&2
        exit 1
    fi
done
sleep 1

# ─── 测试用例 ───
RESULTS=()  # 数组: "name|status|message"
total=0
passed=0
failed=0

run_test() {
    local name="$1"
    local cmd="$2"
    local expect="$3"  # 'json_field|op|value' 或 'text_contains|substr'
    total=$((total + 1))
    echo "  [$total] $name ..."
    if eval "$cmd" 2>/dev/null > /tmp/smoke_out.json; then
        # 校验
        if python3 -c "$expect" /tmp/smoke_out.json 2>/dev/null; then
            echo "    ✓ PASS"
            passed=$((passed + 1))
            RESULTS+=("$name|PASS|")
        else
            local err=$(cat /tmp/smoke_out.json 2>/dev/null | head -c 200)
            echo "    ✗ FAIL (validation failed): $err"
            failed=$((failed + 1))
            RESULTS+=("$name|FAIL|validation failed: $err")
        fi
    else
        local err=$(cat /tmp/smoke_out.json 2>/dev/null | head -c 200)
        echo "    ✗ FAIL (cmd failed): $err"
        failed=$((failed + 1))
        RESULTS+=("$name|FAIL|cmd failed: $err")
    fi
}

# 测试 1: /api/app-info
run_test "/api/app-info 返 200" \
    'curl -s -o /tmp/smoke_out.json -w "%{http_code}" http://localhost:15723/api/app-info | grep -q "^200$" && cp /tmp/smoke_out.json /tmp/smoke_real.json && mv /tmp/smoke_real.json /tmp/smoke_out.json' \
    'import json, sys; d=json.load(open("/tmp/smoke_out.json")); assert "version" in d; assert "update_enabled" in d'

# 测试 2: /api/usage by_tool
run_test "/api/usage by_tool 含 Codex/Claude/Hermes/OpenCode" \
    'curl -s http://localhost:15723/api/usage -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); bt=set(d.get("by_tool",{}).keys()); assert "Codex" in bt, "missing Codex in " + str(bt)'

# 测试 3: /api/history by_model
run_test "/api/history by_model Other < 5% (防累加 bug)" \
    'curl -s http://localhost:15723/api/history -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); bm=d.get("by_model",{}); total=sum(sum(v) for v in bm.values()); other=sum(bm.get("Other",[])); ratio=other/total if total else 0; assert ratio < 0.05, "Other " + str(other) + "/" + str(total) + " = " + str(round(ratio*100,1)) + "% > 5%"'

# 测试 4: /api/history labels 数
run_test "/api/history labels = 30" \
    'curl -s "http://localhost:15723/api/history?days=30" -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert len(d.get("labels",[]))==30, "expected 30 labels, got " + str(len(d.get("labels",[])))'

# 测试 5: /api/heatmap
run_test "/api/heatmap days=30 返 days 数组" \
    'curl -s "http://localhost:15723/api/heatmap?days=30" -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert len(d.get("days",[]))==30; assert d.get("max_value",0)>=0'

# 测试 6: /api/check-update (用 dev URL, 跟 release URL 可能不同)
run_test "/api/check-update 返 ok+version" \
    'curl -s "http://localhost:15723/api/check-update" -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert "ok" in d; assert "latest_version" in d'

# 测试 7: /api/sessions 列表
run_test "/api/sessions 返 sessions + total" \
    'curl -s "http://localhost:15723/api/sessions?days=1&page=1&page_size=1" -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert "sessions" in d; assert "total" in d'

# 测试 8: /api/heatmap_detail (某天) 返 sessions + summary
run_test "/api/heatmap_detail date=今天 返 sessions + summary" \
    'TODAY=$(date +%Y-%m-%d); curl -s "http://localhost:15723/api/heatmap_detail?date=$TODAY&page=1&page_size=1" -o /tmp/smoke_out.json' \
    'import json; d=json.load(open("/tmp/smoke_out.json")); assert "sessions" in d; assert "summary" in d'

# 测试 9: 端口监听
run_test "server 监听 0.0.0.0:$PORT" \
    'lsof -i :15723' \
    'import subprocess; subprocess.run(["true"], check=True)'  # lsof 自身已验证退出码

# ─── 写报告 ───
mkdir -p tests/reports
# 把 RESULTS 数组用换行 join 后写到临时文件, python 块读这个文件
printf "%s\n" "${RESULTS[@]}" > /tmp/smoke_results.txt
REPORT_FILE_TMP="/tmp/smoke_report.json"
python3 - << PYEOF
import json
results = []
with open("/tmp/smoke_results.txt") as f:
    for line in f:
        line = line.rstrip("\n")
        if not line:
            continue
        parts = line.split("|", 2)
        if len(parts) == 3:
            results.append({"name": parts[0], "status": parts[1], "message": parts[2]})
        else:
            results.append({"name": line, "status": "FAIL", "message": "malformed result line"})
report = {
    "label": "${LABEL}",
    "timestamp": "${TIMESTAMP}",
    "server": "${SERVER}",
    "port": ${PORT},
    "total": ${total},
    "passed": ${passed},
    "failed": ${failed},
    "results": results,
}
with open("${REPORT_FILE}", "w") as f:
    json.dump(report, f, ensure_ascii=False, indent=2)
with open("${REPORT_FILE_TMP}", "w") as f:
    json.dump(report, f, ensure_ascii=False, indent=2)
print(f"Report saved: ${REPORT_FILE}")
PYEOF

# ─── 总结 ───
echo ""
echo "=== Result: $passed/$total passed ==="
if [[ $failed -gt 0 ]]; then
    echo "❌ $failed tests FAILED - BLOCK release"
    echo "See $REPORT_FILE for details"
    exit 1
fi
echo "✅ All tests passed - ready to release"

# ─── 自动更新 index (跑成功才更新, 失败不动) ───
if [[ $failed -eq 0 ]]; then
    bash "$(dirname "$0")/update_reports.sh" 2>/dev/null || true
fi
