#!/bin/bash
# 更新 tests/reports/index.json (从 reports/*.json 聚合)
# 和 tests/reports/index.html (静态模板, JS 拉 index.json 渲染)
#
# 跑完 tests/smoke.sh 后调用:
#   bash tests/update_reports.sh
# 或自动集成: smoke.sh 跑成功时自动调
set -euo pipefail

REPORTS_DIR="tests/reports"
INDEX_JSON="${REPORTS_DIR}/index.json"

# 读所有 reports/*.json (按时间戳升序), 一个个 load (不用 jsonl)
# 因为 pretty-print JSON 跨多行, jsonl 单行 load 失败
python3 - <<PYEOF
import json, os, sys, glob
reports = []
files = sorted(glob.glob("${REPORTS_DIR}/*.json"))
for f in files:
    if os.path.basename(f) == "index.json":
        continue
    try:
        with open(f) as fh:
            reports.append(json.load(fh))
    except Exception as e:
        print(f"skip invalid report {f}: {e}", file=sys.stderr)
reports.sort(key=lambda r: r.get("timestamp", ""))
with open("${INDEX_JSON}", "w") as f:
    json.dump(reports, f, ensure_ascii=False, indent=2)
print(f"Updated ${INDEX_JSON} with {len(reports)} reports")
PYEOF
