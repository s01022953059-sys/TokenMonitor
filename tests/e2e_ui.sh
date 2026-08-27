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
SESSION_ID="019f-test-session-detail-cache"
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

# 构造带大体积无关事件的 Codex 会话，覆盖 macOS 单条调用详情的真实慢路径。
python3 - "$TMP_DIR/home" "$SESSION_ID" <<'PY'
import json
import os
import sys

home, session_id = sys.argv[1:]
rollout_dir = os.path.join(home, ".codex", "sessions", "2026", "07", "14")
os.makedirs(rollout_dir, exist_ok=True)
rollout_path = os.path.join(rollout_dir, f"rollout-2026-07-14T00-00-00-{session_id}.jsonl")
with open(rollout_path, "w", encoding="utf-8") as stream:
    stream.write(json.dumps({"type": "event_msg", "payload": {"blob": "x" * (512 * 1024)}}) + "\n")
    for index in range(30):
        role = "user" if index % 2 == 0 else "assistant"
        stream.write(json.dumps({
            "type": "response_item",
            "payload": {
                "role": role,
                "content": [{"type": "input_text", "text": f"E2E message {index}"}],
                "timestamp": f"2026-07-14T00:00:{index:02d}Z",
            },
        }) + "\n")
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
CURRENT_VERSION=$(sed -n '/<key>CFBundleShortVersionString<\/key>/{n;s/.*<string>\([^<]*\)<\/string>.*/\1/;p;}' "$ROOT/Info.plist")
test -n "$CURRENT_VERSION"

"$PWCLI" open "http://127.0.0.1:$PORT" --browser msedge --headed >/dev/null
for _ in {1..20}; do
    CENTER_TEXT=$($PWCLI eval "() => document.getElementById('toolDonutSecondary').innerText + '|' + document.getElementById('modelDonutSecondary').innerText")
    if printf '%s\n' "$CENTER_TEXT" | grep -q "调用次数" && printf '%s\n' "$CENTER_TEXT" | grep -q "缓存命中"; then
        break
    fi
    sleep 0.1
done
printf '%s\n' "$CENTER_TEXT" | grep -q "调用次数"
printf '%s\n' "$CENTER_TEXT" | grep -q "缓存命中"

# 主题只跟随系统外观：旧版手动缓存不得生效，系统变化时页面与 Canvas 同步换色。
SYSTEM_THEME=$($PWCLI eval "() => { localStorage.setItem('token-monitor-theme', 'dark'); Object.defineProperty(systemThemeQuery, 'matches', {configurable:true,value:true}); systemThemeQuery.dispatchEvent(new Event('change')); const light = document.documentElement.dataset.theme === 'light' && getThemeVar('--bg-primary') === '#f5f5f0' && toolChartInstance.data.datasets[0].borderColor === '#ffffff'; Object.defineProperty(systemThemeQuery, 'matches', {configurable:true,value:false}); systemThemeQuery.dispatchEvent(new Event('change')); const dark = document.documentElement.dataset.theme === 'dark' && getThemeVar('--bg-primary') === '#050609' && toolChartInstance.data.datasets[0].borderColor === '#0b0d12'; localStorage.removeItem('token-monitor-theme'); document.getElementById('themeToggleBtn').click(); const preview = document.documentElement.dataset.theme === 'light' && localStorage.getItem('token-monitor-theme') === null && document.getElementById('themeToggleBtn').title.includes('恢复跟随系统'); systemThemeQuery.dispatchEvent(new Event('change')); const restored = document.documentElement.dataset.theme === 'dark'; return [light,dark,preview,restored].join('|'); }")
printf '%s\n' "$SYSTEM_THEME" | grep -q 'true|true|true|true'

# 首页二级统计默认折叠；Agent 可展开模型，模型可展开 Agent，百分比按父项计算。
BREAKDOWN=$($PWCLI eval "async () => { const originalFetch = window.fetch; const fixture = {summary:{total_tokens:400,input_tokens:300,input_cached:100,input_uncached:200,output_tokens:100,events_after_dedup:4,date:'$EXPECTED_TODAY'},by_tool:{Codex:{total_tokens:300,input_tokens:220,output_tokens:80},Claude:{total_tokens:100,input_tokens:80,output_tokens:20}},by_model:{'gpt-5.5':150,'glm-5.2':250},by_model_requests:{'gpt-5.5':2,'glm-5.2':2},by_tool_model:{Codex:{'gpt-5.5':100,'glm-5.2':200},Claude:{'gpt-5.5':50,'glm-5.2':50}},recent_events:[]}; window.fetch = async (url, options) => String(url).includes('/api/usage') ? new Response(JSON.stringify(fixture), {status:200,headers:{'Content-Type':'application/json'}}) : originalFetch(url, options); await updateData(); const toolButton = [...document.querySelectorAll('#toolLegendContainer .legend-item')].find(item => item.innerText.includes('Codex')); const toolDetails = document.getElementById(toolButton.getAttribute('aria-controls')); const toolCollapsed = toolButton.getAttribute('aria-expanded') === 'false' && toolDetails.hidden; toolButton.click(); const toolExpanded = toolButton.getAttribute('aria-expanded') === 'true' && !toolDetails.hidden && toolDetails.innerText.includes('glm-5.2') && toolDetails.innerText.includes('66.7%'); const modelButton = [...document.querySelectorAll('#modelLegendContainer .legend-item')].find(item => item.innerText.includes('gpt-5.5')); const modelDetails = document.getElementById(modelButton.getAttribute('aria-controls')); const modelCollapsed = modelButton.getAttribute('aria-expanded') === 'false' && modelDetails.hidden; modelButton.focus(); modelButton.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); const modelExpanded = modelButton.getAttribute('aria-expanded') === 'true' && !modelDetails.hidden && modelDetails.innerText.includes('Codex') && modelDetails.innerText.includes('66.7%') && modelDetails.innerText.includes('Claude') && modelDetails.innerText.includes('33.3%'); window.fetch = originalFetch; return [toolCollapsed,toolExpanded,modelCollapsed,modelExpanded,document.activeElement === modelButton].join('|'); }")
printf '%s\n' "$BREAKDOWN" | grep -q 'true|true|true|true|true'

"$PWCLI" eval "() => document.getElementById('heatmapOpenBtn').click()" >/dev/null
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

# 星期标签必须与日期格的对应行中心重合，不能从月份标题行开始导致整体上移。
# 首次渲染时格子可能还没出来 (eval 返回 "missing" 导致提取为空)，最多重试 5 次。
WEEKDAY_ALIGNMENT=""
for _retry in 1 2 3 4 5; do
    WEEKDAY_ALIGNMENT_OUTPUT=$($PWCLI eval "() => { const label = [...document.querySelectorAll('.heatmap-weekday-labels span')].find(item => item.innerText.trim() === '一'); const cell = document.querySelector('.heatmap-week:first-child .heatmap-cell-day:first-child'); if (!label || !cell) return 'missing'; const labelRect = label.getBoundingClientRect(); const cellRect = cell.getBoundingClientRect(); return Math.abs((labelRect.top + labelRect.height / 2) - (cellRect.top + cellRect.height / 2)).toFixed(2); }")
    WEEKDAY_ALIGNMENT=$(printf '%s\n' "$WEEKDAY_ALIGNMENT_OUTPUT" | sed -nE 's/^"([0-9.]+)"$/\1/p' | head -1)
    if [ -n "$WEEKDAY_ALIGNMENT" ]; then
        break
    fi
    sleep 1
done
python3 - "$WEEKDAY_ALIGNMENT" <<'PY'
import sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
assert value != "" and value != "missing" and float(value) <= 0.5, f"weekday/grid center offset: {value}px"
PY

# 每日调用详情是 macOS 曾出现长时间卡住的路径：点击当天格子后不能一直停在加载态。
"$PWCLI" eval "() => document.querySelector('.heatmap-cell-day[data-date=\"$EXPECTED_TODAY\"]').click()" >/dev/null
DETAIL_TEXT=""
for _ in {1..20}; do
    DETAIL_TEXT=$("$PWCLI" eval "() => document.getElementById('heatmapDetailList').innerText")
    if ! printf '%s\n' "$DETAIL_TEXT" | grep -q "加载中\|正在整理当天明细"; then
        break
    fi
    sleep 0.2
done
DETAIL_TITLE=$("$PWCLI" eval "() => document.getElementById('heatmapDetailTitle').innerText")
printf '%s\n' "$DETAIL_TITLE" | grep -q "$EXPECTED_TODAY 调用详情"
! printf '%s\n' "$DETAIL_TEXT" | grep -q "加载中\|正在整理当天明细"

# 回归：翻到最后一页不能被废弃的 weekday/hour 监听器覆盖成 undefined:00 或空状态。
"$PWCLI" eval "async () => { const originalFetch = window.fetch; window.fetch = async (url, options) => { const value = String(url); if (value.includes('/api/heatmap_detail?date=')) { const page = Number(new URL(value, location.href).searchParams.get('page') || 1); return new Response(JSON.stringify({cache_state:'ready',date:'$EXPECTED_TODAY',total:2,total_pages:2,page,page_size:50,sessions:[{timestamp:page,time:'07-15 12:00:0' + page,tool:'Codex',model:'gpt-5.6-sol',input_tokens:1,output_tokens:1,total_tokens:2,input_cached:0,latency_ms:0,session_id:'page-' + page}],summary:{total_tokens:2,total_cached:0,call_count:2,avg_latency_ms:0,max_latency_ms:0,peak_tokens:2,peak_time:'07-15 12:00:01'}}), {status:200,headers:{'Content-Type':'application/json'}}); } return originalFetch(url, options); }; await loadHeatmapDayDetail('$EXPECTED_TODAY', 1); document.getElementById('heatmapDetailNextBtn').click(); await new Promise(resolve => setTimeout(resolve, 50)); const title = document.getElementById('heatmapDetailTitle').innerText; const text = document.getElementById('heatmapDetailList').innerText; window.fetch = originalFetch; return title === '$EXPECTED_TODAY 调用详情' && text.includes('12:00:02') && !text.includes('该日暂无调用记录'); }" | grep -q 'true'

# 单条调用详情必须复用服务端快照；首次和再次打开都要走完整 UI 渲染路径。
"$PWCLI" eval "async () => { const started = performance.now(); await loadChatDetail('$SESSION_ID', null, 1); const elapsed = performance.now() - started; const content = document.getElementById('chatDetailContent').innerText; return elapsed < 1000 && content.includes('E2E message 0') && content.includes('E2E message 19'); }" | grep -q 'true'
"$PWCLI" eval "() => document.getElementById('chatDetailModal').classList.remove('active')" >/dev/null
"$PWCLI" eval "async () => { const started = performance.now(); await loadChatDetail('$SESSION_ID', null, 1); const elapsed = performance.now() - started; const content = document.getElementById('chatDetailContent').innerText; return elapsed < 300 && content.includes('E2E message 0') && content.includes('E2E message 19'); }" | grep -q 'true'

# About 必须展示当前版本的简短更新摘要，不能只依赖发布时人工目测。
"$PWCLI" eval "() => { document.getElementById('chatDetailModal').classList.remove('active'); document.getElementById('heatmapDetailModal').classList.remove('active'); }" >/dev/null
"$PWCLI" eval "() => document.getElementById('aboutOpenBtn').click()" >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
printf '%s\n' "$SNAPSHOT" | grep -q "当前版本 v$CURRENT_VERSION"
ABOUT_HIGHLIGHTS=$("$PWCLI" eval "() => Array.from(document.querySelectorAll('#aboutReleaseHighlights li')).map((item) => item.innerText.trim()).filter(Boolean).join('\\n')")
test -n "$ABOUT_HIGHLIGHTS"

# 新版本到达后，About 必须用 Release 的内容替换当前版本摘要，避免误导用户。
"$PWCLI" eval "() => { const savedFetch = window.fetch; window.fetch = async (url, options) => String(url).includes('/api/check-update') ? new Response(JSON.stringify({ok:true,current_version:'$CURRENT_VERSION',latest_version:'99.0.0',update_available:true,notes:'- 独立 worker 扫描\\n- 企业 VPN 代理兼容'}), {status:200,headers:{'Content-Type':'application/json'}}) : savedFetch(url, options); return runUpdateCheck().finally(() => { window.fetch = savedFetch; }); }" >/dev/null
SNAPSHOT=$("$PWCLI" snapshot)
printf '%s\n' "$SNAPSHOT" | grep -q "新版本 v99.0.0"
printf '%s\n' "$SNAPSHOT" | grep -q "独立 worker 扫描"

# 社区页必须由内部内容区滚动，避免 Windows 原生滚动条紧贴整张弹窗边缘。
SCROLL_STYLE=$("$PWCLI" eval "() => { document.getElementById('communityModal').classList.add('active'); const panel = document.querySelector('#communityModal .modal-content'); const header = panel.querySelector('.modal-header'); const body = document.getElementById('communityContainer'); body.innerHTML = '<div style=\"height:1500px\"></div>'; return [getComputedStyle(panel).overflowY, getComputedStyle(body).overflowY, getComputedStyle(body).scrollbarGutter, body.scrollHeight > body.clientHeight, Math.abs(panel.getBoundingClientRect().top - header.getBoundingClientRect().top) < 2].join('|'); }")
printf '%s\n' "$SCROLL_STYLE" | grep -q 'hidden|auto|stable|true|true'

# 强制刷新必须绕过缓存，刷新期间保留已有内容，只让标题栏按钮显示忙碌状态。
COMMUNITY_REFRESH=$("$PWCLI" eval "async () => { const originalFetch = window.fetch; const calls = []; const cachedData = {my_id:'User_E2E0001',my_display_name:'已有排行',can_report:false,total_users:1,today_active_users:1,total_tokens_today:100,my_tokens:100,my_rank:1,rank_total:1,rank_status:'ranked',rank_message:'今日第 1 名',data_status:'ok',leaderboard:[{id:'User_E2E0001',display_name:'已有排行',tokens:100,tool:'Codex',is_me:true}],tool_distribution:{Codex:100}}; localStorage.setItem(COMMUNITY_CACHE_KEY, JSON.stringify({date:communityTodayKey(),saved_at:Date.now(),data:cachedData})); renderCommunity(cachedData); window.fetch = async (url) => { calls.push(String(url)); await new Promise(resolve => setTimeout(resolve, 80)); return new Response(JSON.stringify({...cachedData,my_display_name:'最新排行',leaderboard:[{id:'User_E2E0001',display_name:'最新排行',tokens:100,tool:'Codex',is_me:true}]}), {status:200,headers:{'Content-Type':'application/json'}}); }; document.getElementById('communityRefreshBtn').click(); const retained = document.getElementById('communityContainer').innerText.includes('已有排行') && !document.getElementById('communityContainer').innerText.includes('加载社区数据中'); const busy = document.getElementById('communityRefreshBtn').disabled; await new Promise(resolve => setTimeout(resolve, 180)); const refreshed = calls.some(url => url.includes('/api/community?refresh=1')); const restored = !document.getElementById('communityRefreshBtn').disabled && document.getElementById('communityRefreshBtn').innerText.includes('刷新') && document.getElementById('communityContainer').innerText.includes('最新排行'); window.fetch = originalFetch; return [retained,busy,refreshed,restored].join('|'); }")
printf '%s\n' "$COMMUNITY_REFRESH" | grep -q 'true|true|true|true'

# 排名趋势必须按日期绘制折线；全部出现者参与统计，但画布最多展示 TOP10。
# 第二次打开应直接命中缓存；零用量天保留为 0（折线连续不断开），右侧姓名按最近一个有数据的归档日。
RANK_TREND=$($PWCLI eval "async () => { const originalFetch = window.fetch; const dates = ['2026-07-21','2026-07-22','2026-07-23']; const series = Array.from({length:10}, (_, index) => ({id:'User_' + index,display_name:'用户' + index,total_tokens:(10 - index) * 1000,appearances:2,ranks:[index + 1,index + 1,0],tokens:[100,200,0]})); let fetchCalls = 0; _rankHistory.cache.clear(); localStorage.removeItem(RANK_HISTORY_CACHE_PREFIX + 'week'); window.fetch = async (url, options) => { if (String(url).includes('/api/community/history?range=week')) { fetchCalls += 1; return new Response(JSON.stringify({dates,series,participant_count:12,participant_count_complete:true,data_status:'ok'}), {status:200,headers:{'Content-Type':'application/json'}}); } return originalFetch(url, options); }; document.getElementById('communityModal').classList.remove('active'); document.getElementById('rankHistoryModal').classList.add('active'); await loadRankHistory('week', {forceRefresh:true}); await new Promise(resolve => setTimeout(resolve, 950)); const chart = _rankHistory.chart; const canvas = document.getElementById('rankHistoryChart'); const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data; const nonBlank = pixels.some((value, index) => index % 4 === 3 && value > 0); const latestDataIndex = rankHistoryLatestDataIndex(chart.data.datasets, dates.length); const zeroDayContinuous = latestDataIndex === 2 && chart.data.datasets.every(dataset => dataset.data[2] === 1); const statusHasLatestDay = document.getElementById('rankHistoryStatus').innerText.includes('右侧排名 2026-07-23'); const logScale = chart.options.scales.y.type === 'logarithmic'; const rangeRows = Array.from(document.querySelectorAll('#rankRangeLeaderboard .rank-range-item')); const rangeSummary = rangeRows.length === 10 && rangeRows[0].innerText.includes('用户0') && rangeRows[9].innerText.includes('用户9') && rangeRows[0].querySelector('.rank-range-activity').innerText.includes('2/3') && document.getElementById('rankRangeSummaryMeta').innerText.includes('10 人 · 3 个归档日 · 按累计 Token'); destroyRankHistoryChart(); const started = performance.now(); await loadRankHistory('week'); const cachedFast = performance.now() - started < 100 && fetchCalls === 1 && Boolean(_rankHistory.chart); const labelGap = rankHistoryLabelMinGap(canvas.getContext('2d'), false); const laidOut = layoutRankHistoryEndLabels([{y:100},{y:100},{y:102}], 40, 180, labelGap); const readableGap = labelGap >= 28 && laidOut.every((item, index) => index === 0 || item.labelY - laidOut[index - 1].labelY >= 27.9); const result = [chart && chart.config.type === 'line',chart && chart.data.datasets.length === 10,document.getElementById('rankHistoryStatus').innerText.includes('统计 12 位参与者'),nonBlank,cachedFast,readableGap,zeroDayContinuous,statusHasLatestDay,logScale,rangeSummary].join('|'); window.fetch = originalFetch; return result; }")
printf '%s\n' "$RANK_TREND" | grep -q 'true|true|true|true|true|true|true|true|true|true'
RANK_RENAME=$($PWCLI eval "() => { const cached = readRankHistoryCache('week'); renderCommunity({my_id:'User_ME',my_display_name:'巴乔',can_report:false,total_users:2,today_active_users:2,total_tokens_today:300,my_tokens:100,my_rank:2,rank_total:2,rank_status:'ranked',rank_message:'今日第 2 名',data_status:'ok',member_names:{User_0:'琪琪',User_ME:'巴乔'},leaderboard:[{id:'User_0',display_name:'琪琪',tokens:200,tool:'Codex'},{id:'User_ME',display_name:'巴乔',tokens:100,tool:'Codex',is_me:true}],tool_distribution:{Codex:100}}); renderRankHistory(cached.data); const chartRenamed = _rankHistory.chart.data.datasets.some(dataset => dataset.label === '琪琪') && !_rankHistory.chart.data.datasets.some(dataset => dataset.label === '用户0'); const summaryRenamed = Array.from(document.querySelectorAll('#rankRangeLeaderboard .rank-range-name')).some(node => node.innerText === '琪琪'); return [chartRenamed,summaryRenamed].join('|'); }")
printf '%s\n' "$RANK_RENAME" | grep -q 'true|true'
RANK_SAFE_BOUNDS=$($PWCLI eval "() => { const chart = _rankHistory.chart; const padding = chart.options.layout.padding; const clipOk = chart.data.datasets.every(dataset => dataset && typeof dataset.clip === 'object' && dataset.clip.top === 6); return [padding.top >= 18,padding.right >= 96,padding.bottom >= 10,clipOk].join('|'); }")
printf '%s\n' "$RANK_SAFE_BOUNDS" | grep -q 'true|true|true|true'

echo "[e2e] PASS: 首页 -> 热力图 -> 当日/会话详情 -> About -> 社区刷新 -> TOP10 排名折线"
