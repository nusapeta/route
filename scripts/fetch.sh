#!/usr/bin/env bash
# Download an OSM extract into data/. Usage: scripts/fetch.sh java|indonesia|sumatra
set -euo pipefail
cd "$(dirname "$0")/.."
region="${1:-java}"
mkdir -p data
out="${2:-data/${region}-latest.osm.pbf}"
url="https://download.geofabrik.de/asia/indonesia/${region}-latest.osm.pbf"
[ "$region" = "indonesia" ] && url="https://download.geofabrik.de/asia/indonesia-latest.osm.pbf"
echo "downloading $url"
curl -L --progress-bar -o "$out" "$url"
ls -lh "$out"
