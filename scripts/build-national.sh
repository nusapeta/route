#!/usr/bin/env bash
# Build the nationwide graph (one file, all islands).
# Needs ~5 GB RAM during extraction; GOMEMLIMIT keeps the GC honest.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p data bin
[ -f data/indonesia-latest.osm.pbf ] || scripts/fetch.sh indonesia
go build -o bin/rid-extract ./cmd/rid-extract
GOMEMLIMIT="${GOMEMLIMIT:-5GiB}" bin/rid-extract \
  -pbf data/indonesia-latest.osm.pbf -out data/indonesia.graph
