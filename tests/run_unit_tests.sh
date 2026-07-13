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

echo "[unit] PASS"
