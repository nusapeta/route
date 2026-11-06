#!/usr/bin/env bash
# Download OSM extracts. Usage: scripts/fetch.sh java|indonesia|sumatra|...
set -euo pipefail
region="${1:-java}"
out="${2:-${region}-latest.osm.pbf}"
url="https://download.geofabrik.de/asia/indonesia/${region}-latest.osm.pbf"
[ "$region" = "indonesia" ] && url="https://download.geofabrik.de/asia/indonesia-latest.osm.pbf"
echo "downloading $url"
curl -L --progress-bar -o "$out" "$url"
ls -lh "$out"
