#!/bin/bash
# 第一层：快速、确定的纯逻辑与模块测试。任何失败都阻断后续层级。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "[unit] Python"
python3 -m unittest discover -s tests -p 'test_*.py' -v

if [[ "$(uname)" == "Darwin" ]]; then
    echo "[unit] macOS 无密码更新辅助器"
    bash tests/test_update_helper.sh
fi

echo "[unit] Windows/Go"
(cd go_build && go test ./...)

echo "[unit] 社区中继"
(cd community_relay && go test ./...)

echo "[unit] 前端源代码契约"
node - <<'NODE'
const fs = require('fs');
const plist = fs.readFileSync('Info.plist', 'utf8');
const versionMatch = plist.match(/<key>CFBundleShortVersionString<\/key>\s*<string>([^<]+)<\/string>/);
if (!versionMatch) throw new Error('Info.plist: 无法读取当前版本');
const currentVersion = versionMatch[1].trim();
for (const file of ['index.html', 'go_build/static/index.html']) {
  const html = fs.readFileSync(file, 'utf8');
  const selected = html.match(/<button class="tab-btn active" data-days="(30|90|180|365)">近/);
  const initial = html.match(/let _heatmapDays = (30|90|180|365);/);
  if (!selected || !initial || selected[1] !== initial[1]) {
    throw new Error(`${file}: 热力图默认 Tab 与请求范围不一致`);
  }
  if (!html.includes("const COMMUNITY_SYNC_INTERVAL_MS = 5 * 60 * 1000;") ||
      !html.includes("function communitySyncDue()") ||
      html.includes("data.rank_status !== 'pending' || !data.can_report")) {
    throw new Error(`${file}: 社区静默补报必须覆盖已上榜用户并保持 5 分钟节流`);
  }
  if (!html.includes('id="communityRefreshBtn"') ||
      !html.includes("loadCommunity({forceRefresh: true})") ||
      !html.includes("'?refresh=1&_=' + Date.now()")) {
    throw new Error(`${file}: 社区页缺少绕过缓存的强制刷新入口`);
  }
  if (!html.includes("刚产生的用量可能暂时与首页略有差异，通常几分钟内会自动更新。")) {
    throw new Error(`${file}: 缺少社区数据短暂延迟的友好说明`);
  }
  if (!html.includes("今日活跃用户") || !html.includes("总用户") ||
      !html.includes("去重后的用户数") || !html.includes("已产生用量的用户数") ||
      !html.includes("data.today_active_users") || !html.includes("data.total_users")) {
    throw new Error(`${file}: 社区总用户与今日活跃用户口径展示缺失`);
  }
  if (!html.includes("data.cache_state === 'failed'")) {
    throw new Error(`${file}: 每日调用详情失败状态不能无限停在加载中`);
  }
  const heatmapNextHandlers = (html.match(/getElementById\('heatmapDetailNextBtn'\)\.addEventListener/g) || []).length;
  if (heatmapNextHandlers !== 1) {
    throw new Error(`${file}: 热力图详情分页按钮不能重复绑定旧逻辑`);
  }
  const versionHighlight = new RegExp(`['\"]${currentVersion}['\"]\\s*:\\s*\\[\\s*['\"][^'\"]+['\"]`, 's');
  if (!html.includes('id="aboutReleaseHighlights"') ||
      !html.includes('function renderAboutReleaseHighlights') ||
      !versionHighlight.test(html)) {
    throw new Error(`${file}: 当前版本缺少 About 更新摘要`);
  }
  if (!html.includes('data-desktop-shell="true"') ||
      !html.includes('window.__TOKEN_MONITOR_DESKTOP__')) {
    throw new Error(`${file}: 双端单一内容面标识缺失`);
  }
  if (!html.includes('id="toolDonutSecondary"') ||
      !html.includes('id="modelDonutSecondary"') ||
      !html.includes('缓存命中 ${cacheHitRate}%') ||
      !html.includes('调用次数 ${requestCount.toLocaleString')) {
  if (!html.includes('工具维度保留所有非零工具') ||
      !html.includes('if (data.by_tool[t].total_tokens > 0)')) {
    throw new Error(`${file}: 工具占比不能把低用量工具隐藏到 Other`);
  }
  if (!html.includes('function renderExpandableLegendItem') ||
      !html.includes('data.by_tool_model || {}') ||
      !html.includes("button.setAttribute('aria-expanded'") ||
      !html.includes('expandedLegendItems') ||
      !html.includes("displayModel = Object.prototype.hasOwnProperty.call(modelMajor, model) ? model : 'Other'")) {
    throw new Error(`${file}: 首页缺少默认折叠的 Agent↔模型二级用量统计`);
  }
  if (!html.includes('heatmapDetailToolFilter') ||
      !html.includes('heatmapDetailModelFilter') ||
      !html.includes('heatmapDetailStartTime') ||
      !html.includes('heatmapDetailEndTime') ||
      !html.includes('filter_options') ||
      !html.includes('start_time') ||
      !html.includes('end_time')) {
    throw new Error(`${file}: 调用详情缺少按工具/模型/时间筛选能力`);
  }
  if (!html.includes('class="community-scroll-region"') ||
      !html.includes('#communityModal .modal-content') ||
      !html.includes('scrollbar-gutter: stable') ||
      !html.includes('overflow-y: auto')) {
    throw new Error(`${file}: 社区弹窗缺少内部滚动区或跨平台滚动条样式`);
  }
  // v1.5.0 组队功能：必须包含创建/加入/组队排行核心元素与函数
  if (!html.includes('function renderGroupLine') ||
      !html.includes('function renderGroupRanking') ||
      !html.includes('function showGroupDialog') ||
      !html.includes('function leaveGroup') ||
      !html.includes('showGroupDialog(\'create\')') ||
      !html.includes('showGroupDialog(\'join\')') ||
      !html.includes('已加入') ||
      !html.includes('当前在公共池') ||
      !html.includes('🏆 组队排行')) {
    throw new Error(`${file}: 组队功能核心 UI 元素缺失 (v1.5.0+)`);
  }
  }
  const scripts = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi)];
  scripts.forEach((match) => new Function(match[1]));
}
const swift = fs.readFileSync('app_wrapper.swift', 'utf8');
const windowsGUI = fs.readFileSync('go_build/gui_windows.go', 'utf8');
if (!swift.includes('window.__TOKEN_MONITOR_DESKTOP__ = true') ||
    !swift.includes('webView.topAnchor.constraint(equalTo: window.contentView!.topAnchor)')) {
  throw new Error('macOS: 单一内容面宿主约束缺失');
}
if (!swift.includes('proc.executableURL = URL(fileURLWithPath: "/usr/bin/python3")') ||
    !swift.includes('func applicationWillTerminate') ||
    !swift.includes('stopLocalServer()')) {
  throw new Error('macOS: Python 后端必须由主进程直接托管并在退出时回收');
}
if (!windowsGUI.includes('/?desktop=1') ||
    !windowsGUI.includes('func startTrayUsageLoop') ||
    !windowsGUI.includes('systray.SetTitle("🔥" + formatTrayTokens')) {
  throw new Error('Windows: 单一内容面或实时托盘标题缺失');
}
NODE
cmp -s index.html go_build/static/index.html

echo "[unit] PASS"
