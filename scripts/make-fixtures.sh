#!/usr/bin/env bash
# Rebuild a small graph fixture for nusapeta/stack from a PBF.
#   scripts/make-fixtures.sh <mini.osm.pbf> <out.graph>
set -euo pipefail
pbf=${1:?usage: $0 <mini.osm.pbf> <out.graph>}
out=${2:?usage: $0 <mini.osm.pbf> <out.graph>}
cd "$(dirname "$0")/.."
go run ./cmd/nusa-extract -pbf "$pbf" -out "$out"
