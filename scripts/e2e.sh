#!/usr/bin/env bash
# CI budget guard. Generates a small deterministic graph, runs the real server,
# measures VmRSS after an idle wait and after 200 sequential routes, then checks
# both against testdata/rss-baseline.json with scripts/budget.sh.
#
#   IDLE_SECONDS=5 scripts/e2e.sh        # short wait for CI
#   scripts/e2e.sh                        # 30 s idle wait
set -euo pipefail
cd "$(dirname "$0")/.."

baseline=testdata/rss-baseline.json
graph=testdata/java-mini.graph
binary=bin/nusa-serve
image=${NUSAPETA_IMAGE:-nusapeta/route:ci}
idle_seconds=${IDLE_SECONDS:-30}
port=${E2E_PORT:-18089}
from="-6.2088,106.8456"
to="-6.4484,107.0844"
pid=""

cleanup() {
    if [[ -n $pid ]]; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
    rm -f "$graph"
}
trap cleanup EXIT

command -v go >/dev/null || { echo "missing go toolchain" >&2; exit 2; }
command -v curl >/dev/null || { echo "missing curl" >&2; exit 2; }

echo "== budget guard self-test =="
bash scripts/budget.sh --baseline "$baseline" --self-test

echo "== build fixture and server =="
mkdir -p bin testdata
go build -o "$binary" ./cmd/nusa-serve
go run ./testdata/genfixture -out "$graph"

echo "== start server on 127.0.0.1:$port =="
"$binary" -graph "$graph" -addr "127.0.0.1:$port" -rate 0 -max-route-km 0 >/tmp/nusa-budget.log 2>&1 &
pid=$!
ready=0
for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/healthz" >/dev/null; then ready=1; break; fi
    sleep 0.2
done
if [[ $ready != 1 ]]; then
    echo "server did not become ready" >&2
    cat /tmp/nusa-budget.log >&2
    exit 1
fi

rss_mb() {
    awk '/^VmRSS:/{printf "%.1f", $2/1024}' "/proc/$pid/status"
}

echo "== idle RSS after ${idle_seconds}s =="
sleep "$idle_seconds"
idle_mb=$(rss_mb)
bash scripts/budget.sh --baseline "$baseline" --rss-mb "$idle_mb" --phase idle \
    --binary "$binary" --image "$image"

echo "== RSS after 200 sequential routes =="
for _ in $(seq 1 200); do
    curl -sf -o /dev/null "http://127.0.0.1:$port/route?from=$from&to=$to&simplify=100"
done
loaded_mb=$(rss_mb)
bash scripts/budget.sh --baseline "$baseline" --rss-mb "$loaded_mb" --phase loaded --check-only

echo "e2e budget guard: PASS (idle ${idle_mb} MB, loaded ${loaded_mb} MB)"
