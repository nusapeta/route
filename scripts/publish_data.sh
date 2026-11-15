#!/usr/bin/env bash
# Publish data artifacts to a GitHub release. Run from the repo root after the
# artifacts have been verified locally.
#
#   scripts/publish_data.sh data-2026.11 ../artifacts/route-data-2026.11/*
#
# Every upload gets a sibling .sha256. PROVENANCE.txt and LICENSE-DATA are
# picked up from the directory of the first artifact so a release never ships
# data without its source, build command, and licence.
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

dir=$(dirname "$1")
files=()
for f in "$@"; do
  [[ $f == *.sha256 ]] && continue
  case $(basename "$f") in
  PROVENANCE.txt | LICENSE-DATA) continue ;;
  esac
  files+=("$f")
done
for extra in PROVENANCE.txt LICENSE-DATA; do
  [[ -f $dir/$extra ]] || { echo "missing $dir/$extra" >&2; exit 2; }
  files+=("$dir/$extra")
done

if ! gh release view "$tag" >/dev/null 2>&1; then
  gh release create "$tag" --title "Data $tag" --notes "$(cat <<EOF
OSM-derived routing graph for Nusapeta Route, release \`$tag\`.

- **Licence**: ODbL 1.0, © OpenStreetMap contributors. See \`LICENSE-DATA\`.
- **Provenance**: source PBF URL and SHA-256, extractor commit, build command,
  and build date are in \`PROVENANCE.txt\`.
- **Integrity**: every artifact has a sibling \`.sha256\`; verify with
  \`sha256sum -c <file>.sha256\`.
- **Format**: RIDG graph v5 (see \`docs/ARCHITECTURE.md\`).

Publish a new tag instead of replacing an asset. Readers cache these URLs.
EOF
)"
fi

sums=()
for f in "${files[@]}"; do
  [[ $f == *.sha256 ]] && continue
  # Bare basename so sha256sum -c works after download, when the artifact sits
  # next to its checksum instead of in the build directory.
  (cd "$(dirname "$f")" && sha256sum "$(basename "$f")") > "$f.sha256"
  sums+=("$f.sha256")
done

gh release upload "$tag" "${files[@]}" "${sums[@]}" --clobber
gh release view "$tag" --json assets -q '.assets[].name'
