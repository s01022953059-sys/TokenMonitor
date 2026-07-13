#!/bin/bash
# 兼容旧命令。发布流程已迁移到 tests/api_contract.py。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="python"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --go) BACKEND="go"; shift ;;
        --version) shift 2 ;;
        *) echo "Unknown arg: $1" >&2; exit 2 ;;
    esac
done
exec python3 "$ROOT/tests/api_contract.py" --backend "$BACKEND"
