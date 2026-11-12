#!/usr/bin/env bash
# Quick end-to-end check against a running server.
# Usage: scripts/smoke.sh [base-url]   (default http://127.0.0.1:8080)
# Public gateway blocks /metrics; set METRICS_BASE=http://127.0.0.1:18080.
set -euo pipefail
base="${1:-http://127.0.0.1:8080}"
metrics_base="${METRICS_BASE:-$base}"
from="${2:--6.2088,106.8456}"   # Jakarta
to="${3:--6.2297,106.8166}"

echo "== healthz =="
curl -sf "$base/healthz" | head -c 400; echo

echo "== GET polyline =="
curl -sf "$base/route?from=$from&to=$to&format=polyline" | head -c 300; echo

echo "== POST json =="
curl -sf -X POST -H 'Content-Type: application/json' \
  -d "{\"from\":\"$from\",\"to\":\"$to\",\"format\":\"polyline\"}" \
  "$base/route" | head -c 120; echo

echo "== CORS preflight =="
curl -sf -X OPTIONS -o /dev/null -D- "$base/route" | grep -i access-control-allow-origin

echo "== headers =="
curl -sf -o /dev/null -D- "$base/route?from=$from&to=$to" | grep -iE "x-route|etag|cache-control"

echo "== etag revalidation (expect 304) =="
etag=$(curl -sf -o /dev/null -D- "$base/route?from=$from&to=$to" | awk 'tolower($1)=="etag:"{print $2}' | tr -d '\r')
code=$(curl -s -o /dev/null -w '%{http_code}' -H "If-None-Match: $etag" "$base/route?from=$from&to=$to")
echo "If-None-Match: $etag -> $code"

echo "== metrics =="
curl -sf "$metrics_base/metrics" | grep -E "^route_(requests_total|request_duration_seconds_count|inflight|expansions_total)" | head -8

echo "== rate limit (a burst over the configured -rate must give 429) =="
for i in $(seq 1 12); do curl -s -o /dev/null -w '%{http_code} ' "$base/route?from=$from&to=$to"; done; echo
