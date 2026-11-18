#!/usr/bin/env bash
# Compare measured resource numbers against the hard limits in the budget table
# of docs/ROADMAP.md, and against a recorded baseline so a regression is red.
# Manual by default: the national graph, image and extraction are not in CI.
#
#   scripts/budget.sh                         # graph + local image + running server
#   scripts/budget.sh --graph data/indonesia.graph --image nusapeta/route:local
#   scripts/budget.sh --pid 1234              # measure that server's idle RSS
#   scripts/budget.sh --time-extract          # also rebuild the national graph
#   scripts/budget.sh --baseline testdata/rss-baseline.json --phase idle \
#       --rss-mb 15.0 --check-only            # guard logic only (used by tests)
#   scripts/budget.sh --self-test             # prove the 5 MB regression is red
#
# A metric that cannot be measured prints `skip` and does not fail the run.
# Exit status is 1 when a measured metric breaches a hard limit or the
# baseline delta, or when a self-test case does not behave as expected.
set -euo pipefail
cd "$(dirname "$0")/.."

rss_max_mb=32        # ROADMAP: RSS idle nasional, hard
image_max_mb=40      # ROADMAP: image container multi-stage, hard
artifact_max_mb=2048 # ROADMAP: artefak nasional, hard
binary_max_mb=12     # ROADMAP: binary statis, hard
extract_max_s=600    # ROADMAP: ekstraksi nasional, hard
delta_limit_mb=2     # PRD PR-01: RSS regression gate

graph=data/indonesia.graph
image=nusapeta/route:local
pid=${NUSAPETA_PID:-}
time_extract=0
pbf=data/indonesia-latest.osm.pbf
baseline=""
binary=""
rss_mb=""
rss_phase=idle
check_only=0
self_test=0

while [[ $# -gt 0 ]]; do
    case $1 in
    --graph) graph=$2; shift 2 ;;
    --image) image=$2; shift 2 ;;
    --pid) pid=$2; shift 2 ;;
    --pbf) pbf=$2; shift 2 ;;
    --baseline) baseline=$2; shift 2 ;;
    --binary) binary=$2; shift 2 ;;
    --rss-mb) rss_mb=$2; shift 2 ;;
    --phase) rss_phase=$2; shift 2 ;;
    --check-only) check_only=1; shift ;;
    --self-test) self_test=1; shift ;;
    --time-extract) time_extract=1; shift ;;
    -h | --help) sed -n '2,17p' "$0" | cut -c3-; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

# jsonNum reads one numeric field from a flat JSON object. The baseline is
# generated in-repo, so a regex is enough and the script keeps no jq dependency.
jsonNum() {
    awk -v k="$1" '
        index($0, "\"" k "\"") {
            line = $0
            sub(/.*:[[:space:]]*/, "", line)
            sub(/[^0-9.].*/, "", line)
            print line
            exit
        }' "$baseline"
}

if [[ -n $baseline ]]; then
    [[ -f $baseline ]] || { echo "missing baseline: $baseline" >&2; exit 2; }
    v=$(jsonNum rss_hard_mb); [[ -n $v ]] && rss_max_mb=$v
    v=$(jsonNum image_max_mb); [[ -n $v ]] && image_max_mb=$v
    v=$(jsonNum binary_max_mb); [[ -n $v ]] && binary_max_mb=$v
    v=$(jsonNum artifact_max_mb); [[ -n $v ]] && artifact_max_mb=$v
    v=$(jsonNum delta_limit_mb); [[ -n $v ]] && delta_limit_mb=$v
fi

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

# guardRSS checks the hard ceiling and the delta from the recorded baseline.
# A missing baseline only disables the delta, never the hard limit.
guardRSS() {
    local name=$1 measured=$2 base=${3:-}
    [[ -n $measured ]] || { skip "$name"; return; }
    check "$name" "$measured" "$rss_max_mb" MB
    if [[ -n $base ]]; then
        local delta
        delta=$(awk -v m="$measured" -v b="$base" 'BEGIN{d=m-b; if(d<0)d=-d; printf "%.1f", d}')
        check "$name delta" "$delta" "$delta_limit_mb" MB
    fi
}

base_rss=""
if [[ -n $baseline ]]; then
    if [[ $rss_phase == loaded ]]; then
        base_rss=$(jsonNum loaded_rss_mb)
    else
        base_rss=$(jsonNum idle_rss_mb)
    fi
fi

if [[ $self_test == 1 ]]; then
    [[ -n $baseline ]] || { echo "--self-test needs --baseline" >&2; exit 2; }
    selftest_fail=0
    selfCheck() {
        local desc=$1 measured=$2 base=$3 expect=$4 out got
        fail=0
        out=$(guardRSS selftest "$measured" "$base" 2>&1) || true
        if [[ $out == *FAIL* ]]; then got=1; else got=0; fi
        if [[ $got == "$expect" ]]; then
            printf 'PASS  %-26s %s\n' "$desc" "$(echo "$out" | tail -1)"
        else
            printf 'FAIL  %-26s %s\n' "$desc" "$(echo "$out" | tail -1)"
            selftest_fail=1
        fi
    }
    idle=$(jsonNum idle_rss_mb)
    selfCheck "neutral idle" "$idle" "$idle" 0
    selfCheck "neutral loaded" "$(jsonNum loaded_rss_mb)" "$(jsonNum loaded_rss_mb)" 0
    selfCheck "plus 5 MB allocation" "$(awk -v v="$idle" 'BEGIN{printf "%.1f", v+5}')" "$idle" 1
    selfCheck "over hard limit" "$(awk -v v="$rss_max_mb" 'BEGIN{printf "%.1f", v+1}')" "$idle" 1
    [[ $selftest_fail == 0 ]] || { echo "budget guard self-test: FAIL" >&2; exit 1; }
    echo "budget guard self-test: PASS"
    exit 0
fi

# Binary size, when a build is available.
if [[ -n $binary && -f $binary ]]; then
    binary_b=$(stat -c %s "$binary")
    check "binary $(basename "$binary")" "$(awk -v b="$binary_b" 'BEGIN{print b/1048576}')" "$binary_max_mb" MB
else
    skip "binary"
fi

# RSS idle, from the kernel's view of a running server.
if [[ -n $rss_mb ]]; then
    guardRSS "$rss_phase RSS" "$rss_mb" "$base_rss"
elif [[ -z $pid ]] && command -v pgrep >/dev/null; then
    pid=$(pgrep -x nusa-serve | head -1 || true)
fi
if [[ -z $rss_mb ]]; then
    if [[ -n $pid && -r /proc/$pid/status ]]; then
        rss_kb=$(awk '/^VmRSS:/{print $2}' "/proc/$pid/status")
        if [[ -n ${rss_kb:-} ]]; then
            guardRSS "$rss_phase RSS (pid $pid)" "$(awk -v k="$rss_kb" 'BEGIN{print k/1024}')" "$base_rss"
        else
            skip "RSS"
        fi
    else
        skip "RSS"
    fi
fi

if [[ $check_only == 0 ]]; then
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
fi

if [[ $fail == 1 ]]; then
    echo "budget guard: over hard limit or baseline delta" >&2
    exit 1
fi
echo "budget guard: within hard limits"
