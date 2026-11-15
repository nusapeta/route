#!/usr/bin/env bash
# Compare measured resource numbers against the hard limits in the budget table
# of docs/ROADMAP.md. Manual, before a release: it wants the national graph and
# (optionally) a fresh national extraction, so it never runs in CI.
#
#   scripts/budget.sh                      # graph + local image + running server
#   scripts/budget.sh --graph data/indonesia.graph --image nusapeta/route:local
#   scripts/budget.sh --pid 1234           # measure that server's idle RSS
#   scripts/budget.sh --time-extract       # also rebuild the national graph
#
# A metric that cannot be measured prints `skip` and does not fail the run.
# Exit status is 1 when a measured metric is over its hard limit.
set -euo pipefail
cd "$(dirname "$0")/.."

rss_max_mb=32        # ROADMAP: RSS idle nasional
image_max_mb=40      # ROADMAP: image container multi-stage
artifact_max_mb=2048 # ROADMAP: artefak nasional
extract_max_s=600    # ROADMAP: ekstraksi nasional

graph=data/indonesia.graph
image=nusapeta/route:local
pid=${NUSAPETA_PID:-}
time_extract=0
pbf=data/indonesia-latest.osm.pbf

while [[ $# -gt 0 ]]; do
    case $1 in
    --graph) graph=$2; shift 2 ;;
    --image) image=$2; shift 2 ;;
    --pid) pid=$2; shift 2 ;;
    --pbf) pbf=$2; shift 2 ;;
    --time-extract) time_extract=1; shift ;;
    -h | --help) sed -n '2,15p' "$0" | cut -c3-; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

fail=0
printf '%-22s %12s %12s  %s\n' metric measured limit verdict

# check NAME MEASURED LIMIT UNIT — prints one row, records a hard-limit breach.
check() {
    local name=$1 value=$2 limit=$3 unit=$4
    local verdict=ok
    if awk -v v="$value" -v l="$limit" 'BEGIN{exit !(v > l)}'; then
        verdict=FAIL
        fail=1
    fi
    printf '%-22s %9.1f %-2s %9.1f %-2s  %s\n' "$name" "$value" "$unit" "$limit" "$unit" "$verdict"
}

# skip NAME — metric not measurable on this machine.
skip() {
    printf '%-22s %12s %12s  skip\n' "$1" - -
}

# RSS idle, from the kernel's view of a running server.
if [[ -z $pid ]] && command -v pgrep >/dev/null; then
    pid=$(pgrep -x nusa-serve | head -1 || true)
fi
if [[ -n $pid && -r /proc/$pid/status ]]; then
    rss_kb=$(awk '/^VmRSS:/{print $2}' "/proc/$pid/status")
    if [[ -n ${rss_kb:-} ]]; then
        check "idle RSS (pid $pid)" "$(awk -v k="$rss_kb" 'BEGIN{print k/1024}')" "$rss_max_mb" MB
    else
        skip "idle RSS"
    fi
else
    skip "idle RSS"
fi

# Image size as the registry and docker report it (unpacked bytes).
if command -v docker >/dev/null && docker image inspect "$image" >/dev/null 2>&1; then
    image_b=$(docker image inspect -f '{{.Size}}' "$image")
    check "image $image" "$(awk -v b="$image_b" 'BEGIN{print b/1048576}')" "$image_max_mb" MB
else
    skip "image"
fi

# National artifact, measured on the decompressed graph the server maps.
if [[ -f $graph ]]; then
    graph_b=$(stat -c %s "$graph")
    check "national artifact" "$(awk -v b="$graph_b" 'BEGIN{print b/1048576}')" "$artifact_max_mb" MB
else
    skip "national artifact"
fi

# National extraction, timed on this machine. Needs the PBF and ~5 GB RAM.
if [[ $time_extract == 1 ]]; then
    [[ -f $pbf ]] || { echo "missing PBF: $pbf" >&2; exit 2; }
    command -v go >/dev/null || { echo "missing go toolchain" >&2; exit 2; }
    mkdir -p bin data
    go build -o bin/nusa-extract ./cmd/nusa-extract
    tmp=$(mktemp -p data budget-XXXXXX.graph)
    trap 'rm -f "$tmp"' EXIT
    start=$(date +%s)
    GOMEMLIMIT="${GOMEMLIMIT:-5GiB}" bin/nusa-extract -pbf "$pbf" -out "$tmp" >/dev/null
    elapsed=$(($(date +%s) - start))
    check "national extraction" "$elapsed" "$extract_max_s" s
else
    skip "national extraction"
fi

if [[ $fail == 1 ]]; then
    echo "budget guard: over hard limit" >&2
    exit 1
fi
echo "budget guard: within hard limits"
