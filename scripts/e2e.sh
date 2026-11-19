#!/usr/bin/env bash
# CI budget guard. Absolute RSS is runner-dependent and noisy, so the same
# server is built from main and from the PR on the same runner. Each build is
# sampled several times; the minimum VmRSS per phase is compared with a 2 MB
# threshold. A PR with a resident 5 MB global allocation shifts every sample
# by ~4-5 MB and fails; a neutral PR compares equal.
#
#   IDLE_SECONDS=2 scripts/e2e.sh       # short warm-up for CI
#   BASE_REF=HEAD scripts/e2e.sh        # compare the checkout with itself
set -euo pipefail
cd "$(dirname "$0")/.."

baseline=testdata/rss-baseline.json
graph=testdata/java-mini.graph
image=${NUSAPETA_IMAGE:-nusapeta/route:ci}
base_ref=${BASE_REF:-origin/main}
idle_seconds=${IDLE_SECONDS:-2}
samples=${SAMPLES:-5}
from="-6.2088,106.8456"
to="-6.4484,107.0844"
base_dir=""
running_pid=""

cleanup() {
    if [[ -n $running_pid ]]; then
        kill "$running_pid" 2>/dev/null || true
        wait "$running_pid" 2>/dev/null || true
    fi
    if [[ -n $base_dir && -d $base_dir ]]; then
        git worktree remove --force "$base_dir" >/dev/null 2>&1 || rm -rf "$base_dir"
    fi
    rm -f "$graph"
}
trap cleanup EXIT

for cmd in go git curl sort; do
    command -v "$cmd" >/dev/null || { echo "missing $cmd" >&2; exit 2; }
done

echo "== budget guard self-test =="
bash scripts/budget.sh --baseline "$baseline" --self-test

echo "== build fixture and PR server =="
mkdir -p bin testdata
go build -o bin/nusa-serve ./cmd/nusa-serve
go run ./testdata/genfixture -out "$graph"
graph_abs=$(realpath "$graph")

echo "== build base server from $base_ref =="
base_dir=$(mktemp -d)
git worktree add --detach "$base_dir" "$base_ref" >/dev/null
(cd "$base_dir" && go build -o nusa-serve ./cmd/nusa-serve)

rss_mb() {
    awk '/^VmRSS:/{printf "%.1f", $2/1024}' "/proc/$1/status"
}

# sample_once LABEL BINARY PORT — prints "idle loaded" in MiB for one process.
sample_once() {
    local label=$1 bin=$2 port=$3 j ready idle loaded
    "$bin" -graph "$graph_abs" -addr "127.0.0.1:$port" -rate 0 -max-route-km 0 \
        >"/tmp/nusa-budget-$label-$port.log" 2>&1 &
    running_pid=$!
    ready=0
    for j in $(seq 1 50); do
        if curl -sf "http://127.0.0.1:$port/healthz" >/dev/null; then ready=1; break; fi
        sleep 0.2
    done
    if [[ $ready != 1 ]]; then
        echo "$label server did not become ready" >&2
        cat "/tmp/nusa-budget-$label-$port.log" >&2
        exit 1
    fi
    sleep "$idle_seconds"
    idle=$(rss_mb "$running_pid")
    for j in $(seq 1 200); do
        curl -sf -o /dev/null "http://127.0.0.1:$port/route?from=$from&to=$to&simplify=100"
    done
    loaded=$(rss_mb "$running_pid")
    kill "$running_pid" 2>/dev/null || true
    wait "$running_pid" 2>/dev/null || true
    running_pid=""
    printf '%s %s\n' "$idle" "$loaded"
}

minimum() {
    printf '%s\n' "$@" | sort -n | head -1
}

echo "== measure base and PR (interleaved, n=$samples) =="
declare -a bi=() bl=() hi=() hl=()
for i in $(seq 1 "$samples"); do
    out=$(sample_once base "$base_dir/nusa-serve" $((18100 + i)))
    read -r idle loaded <<<"$out"
    bi+=("$idle"); bl+=("$loaded")
    out=$(sample_once head "$PWD/bin/nusa-serve" $((18200 + i)))
    read -r idle loaded <<<"$out"
    hi+=("$idle"); hl+=("$loaded")
done
base_idle=$(minimum "${bi[@]}")
base_loaded=$(minimum "${bl[@]}")
head_idle=$(minimum "${hi[@]}")
head_loaded=$(minimum "${hl[@]}")
echo "base: min idle ${base_idle} MB, min loaded ${base_loaded} MB"
echo "head: min idle ${head_idle} MB, min loaded ${head_loaded} MB"

echo "== compare against base on this runner =="
bash scripts/budget.sh --baseline "$baseline" --rss-mb "$head_idle" --base-rss "$base_idle" \
    --phase idle --binary bin/nusa-serve --image "$image"
bash scripts/budget.sh --baseline "$baseline" --rss-mb "$head_loaded" --base-rss "$base_loaded" \
    --phase loaded --check-only

echo "e2e budget guard: PASS"
