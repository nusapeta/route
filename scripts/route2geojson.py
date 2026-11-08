#!/usr/bin/env python3
"""Konversi respons /route jadi GeoJSON untuk dicek visual (geojson.io).

Pakai:
    curl -s "http://127.0.0.1:8080/route?from=-6.2,106.8&to=-6.23,106.82" > r.json
    scripts/route2geojson.py r.json > r.geojson     # atau: ... | scripts/route2geojson.py -

Mendukung respons {"points": [[lat,lng],...]}, {"polyline": "..."}, dan
{"polyline_b64": "..."} lewat scripts/polyline_decode.py.
Juga mencetak ringkasan (jumlah titik, bbox, jarak, ujung) ke stderr.
"""
import json
import sys

from polyline_decode import from_json


def main():
    src = sys.argv[1] if len(sys.argv) > 1 else "-"
    raw = sys.stdin.read() if src == "-" else open(src).read()
    d = json.loads(raw)
    if "error" in d:
        sys.exit(f"server error: {d['error']}")

    pts, _ = from_json(d)

    lats = [p[0] for p in pts]
    lons = [p[1] for p in pts]
    print(f"titik      : {len(pts)} (server points_n={d.get('points_n')})", file=sys.stderr)
    print(f"bbox       : [{min(lats):.6f}, {min(lons):.6f}, {max(lats):.6f}, {max(lons):.6f}]", file=sys.stderr)
    if "bbox" in d:
        print(f"bbox server: {[round(x, 6) for x in d['bbox']]}", file=sys.stderr)
    print(f"awal/akhir : {pts[0]} -> {pts[-1]}", file=sys.stderr)
    if "distance_m" in d:
        print(f"jarak      : {d['distance_m']} m, durasi {d.get('duration_s', 0)/60:.1f} mnt", file=sys.stderr)
    if "from" in d:
        print(f"echo from/to: {d['from']} -> {d['to']}", file=sys.stderr)

    fc = {
        "type": "FeatureCollection",
        "features": [
            {
                "type": "Feature",
                "properties": {"kind": "route", "distance_m": d.get("distance_m")},
                "geometry": {"type": "LineString", "coordinates": [[p[1], p[0]] for p in pts]},
            },
            {
                "type": "Feature",
                "properties": {"kind": "start"},
                "geometry": {"type": "Point", "coordinates": [pts[0][1], pts[0][0]]},
            },
            {
                "type": "Feature",
                "properties": {"kind": "end"},
                "geometry": {"type": "Point", "coordinates": [pts[-1][1], pts[-1][0]]},
            },
        ],
    }
    json.dump(fc, sys.stdout, indent=1)


if __name__ == "__main__":
    main()
