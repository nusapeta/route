#!/usr/bin/env bash
# Short car/motor A/B for one built region. Starts both servers on loopback,
# runs a few fixed pairs, writes data/<region>-ab.csv and -ab.txt, and always
# stops the servers. Missing pairs are reported, not invented.
set -euo pipefail
cd "$(dirname "$0")/.."

region="${1:-java}"
limit="${AB_LIMIT:-2}"
mport="${AB_MOBIL_PORT:-18090}"
kport="${AB_MOTOR_PORT:-18091}"
g="data/${region}.graph"
[ -f "$g" ] || {
	echo "refresh-ab: missing $g (run make graph REGION=$region first)" >&2
	exit 1
}

pairs="paper/results/pairs-${region}.csv"
key="$region"
[ "$region" = "nusa-tenggara" ] && key="nusa"
if [ ! -f "$pairs" ]; then
	src="paper/results/pairs-motor-regions.csv"
	pairs="data/${region}-pairs.csv"
	awk -F, -v r="$key" 'NR == 1 || $1 == r' "$src" >"$pairs"
fi
rows=$(awk 'END { print NR - 1 }' "$pairs")
if [ "$rows" -le 0 ]; then
	echo "refresh-ab: no pairs for $region in paper/results/pairs-motor-regions.csv; A/B skipped" >&2
	exit 0
fi

if [ "$mport" = "$kport" ]; then
	echo "refresh-ab: AB_MOBIL_PORT and AB_MOTOR_PORT must differ" >&2
	exit 1
fi
for port in "$mport" "$kport"; do
	if curl -fsS --max-time 1 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; then
		echo "refresh-ab: port ${port} is already serving a server" >&2
		exit 1
	fi
done

tmp=$(mktemp -d)
pids=()
cleanup() {
	for pid in "${pids[@]}"; do
		kill "$pid" 2>/dev/null || true
	done
	for pid in "${pids[@]}"; do
		wait "$pid" 2>/dev/null || true
	done
	rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

bin/nusa-serve -graph "$g" -addr "127.0.0.1:${mport}" -conc 1 -rate 0 -timeout 60s \
	>"$tmp/mobil.log" 2>&1 &
pids+=("$!")
bin/nusa-serve -profile motor -graph "$g" -addr "127.0.0.1:${kport}" -conc 1 -rate 0 -timeout 60s \
	>"$tmp/motor.log" 2>&1 &
pids+=("$!")

wait_health() {
	local port="$1" log="$2" i
	for i in $(seq 1 120); do
		if curl -fsS "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.25
	done
	echo "refresh-ab: server on ${port} did not become healthy" >&2
	cat "$log" >&2
	return 1
}
wait_health "$mport" "$tmp/mobil.log"
wait_health "$kport" "$tmp/motor.log"

scripts/motor-ab.py \
	--pairs "$pairs" \
	--mobil "http://127.0.0.1:${mport}" \
	--motor "http://127.0.0.1:${kport}" \
	--limit "$limit" --pause 0 --timeout 120 \
	--csv "data/${region}-ab.csv" \
	--summary "data/${region}-ab.txt"
echo "refresh-ab: wrote data/${region}-ab.csv and data/${region}-ab.txt"
