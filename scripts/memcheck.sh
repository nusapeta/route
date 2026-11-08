#!/usr/bin/env bash
# Read the server's memory picture: /metrics workspace gauges plus the kernel's
# view of the process. Usage: scripts/memcheck.sh [base-url] [pid]
set -euo pipefail

ADDR="${1:-http://127.0.0.1:8080}"
PID="${2:-}"

echo "=== /metrics workspace + route counters ($ADDR) ==="
curl -sS "$ADDR/metrics" | grep -E "^routeid_workspace_" || true

if [ -z "$PID" ] && command -v pgrep >/dev/null; then
    PID="$(pgrep -x rid-server | head -1 || true)"
fi
[ -n "$PID" ] || { echo "no rid-server pid; pass one as \$2"; exit 0; }

echo "=== process $PID ==="
ps -o pid,rss,vsz,comm -p "$PID" || true
grep -E "^Rss:|^Pss:|^Anonymous:" "/proc/$PID/smaps_rollup" 2>/dev/null || true
