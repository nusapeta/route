#!/usr/bin/env python3
"""Konversi respons /route jadi GeoJSON untuk dicek visual (geojson.io).

Pakai:
    curl -s "http://127.0.0.1:8080/route?from=-6.2,106.8&to=-6.23,106.82" > r.json
    scripts/route2geojson.py r.json > r.geojson     # atau: ... | scripts/route2geojson.py -

Mendukung dua bentuk respons: {"points": [[lat,lng],...]} dan {"polyline": "..."}.
Juga mencetak ringkasan (jumlah titik, bbox, jarak, ujung) ke stderr.
"""
import json
import sys


def decode_polyline(s):
    """Decoder referensi Google (precision 1e-5)."""
    pts, i, lat, lng = [], 0, 0, 0
    while i < len(s):
        vals = []
        for _ in range(2):
            shift = result = 0
            while True:
                b = ord(s[i]) - 63
                i += 1
                result |= (b & 0x1F) << shift
                shift += 5
                if b < 0x20:
                    break
            vals.append(~(result >> 1) if result & 1 else result >> 1)
        lat += vals[0]
        lng += vals[1]
        pts.append([lat / 1e5, lng / 1e5])
    return pts


def main():
    src = sys.argv[1] if len(sys.argv) > 1 else "-"
    raw = sys.stdin.read() if src == "-" else open(src).read()
    d = json.loads(raw)
    if "error" in d:
        sys.exit(f"server error: {d['error']}")

    pts = d.get("points")
    if not pts:
        if "polyline" not in d:
            sys.exit("respons tidak punya 'points' maupun 'polyline'")
        pts = decode_polyline(d["polyline"])

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
