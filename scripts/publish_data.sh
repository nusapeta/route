#!/usr/bin/env bash
# Publish data artifacts to a GitHub release. Run from the repo root after the
# release exists and has been reviewed locally.
#
#   scripts/publish_data.sh data-2026.11 dist/data/*
#
# Every uploaded file gets a sibling .sha256. The script refuses to publish a
# tag that collides with a code release (v*).
set -euo pipefail

tag=${1:-}
if [[ -z $tag || $# -lt 2 ]]; then
  echo "usage: $0 <data-YYYY.MM> <file>..." >&2
  exit 2
fi
shift

if [[ $tag != data-* ]]; then
  echo "refusing tag '$tag': data releases must start with data-" >&2
  exit 2
fi

for f in "$@"; do
  [[ -f $f ]] || { echo "missing file: $f" >&2; exit 2; }
done

if ! gh release view "$tag" >/dev/null 2>&1; then
  gh release create "$tag" --title "Data $tag" \
    --notes "Artefak data ODbL 1.0. Lihat PROVENANCE.txt dan LICENSE-DATA."
fi

sums=()
for f in "$@"; do
  sha256sum "$f" > "$f.sha256"
  sums+=("$f.sha256")
done

gh release upload "$tag" "$@" "${sums[@]}" --clobber
gh release view "$tag" --json assets -q '.assets[].name'
