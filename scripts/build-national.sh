#!/usr/bin/env bash
# Build the nationwide graph. Needs ~10-16 GB RAM during extraction.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f indonesia-latest.osm.pbf ] || scripts/fetch.sh indonesia
go build -o bin/rid-extract ./cmd/rid-extract
bin/rid-extract -pbf indonesia-latest.osm.pbf -out indonesia.graph
