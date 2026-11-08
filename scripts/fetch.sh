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
# -f: 404/5xx must fail instead of writing an HTML error page; download to .tmp
# so a partial file never looks like a finished extract to the Makefile.
curl -fL --retry 3 --progress-bar -o "$out.tmp" "$url" || {
	rm -f "$out.tmp"
	exit 1
}
size=$(wc -c <"$out.tmp")
# A real region extract is megabytes; anything smaller is a proxy/error page.
if [ "$size" -lt 1000000 ]; then
	echo "fetch: $url returned only $size bytes, not a valid pbf" >&2
	rm -f "$out.tmp"
	exit 1
fi
mv "$out.tmp" "$out"
ls -lh "$out"
