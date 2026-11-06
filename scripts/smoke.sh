#!/usr/bin/env bash
# Quick end-to-end check against a running server.
# Usage: scripts/smoke.sh [base-url]   (default http://127.0.0.1:8080)
set -euo pipefail
base="${1:-http://127.0.0.1:8080}"
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
curl -sf -o /dev/null -D- "$base/route?from=$from&to=$to" | grep -i x-route
