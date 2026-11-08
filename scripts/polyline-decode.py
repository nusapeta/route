#!/usr/bin/env python3
"""Decode polyline routeid dengan aman, apa pun bentuk inputnya.

Menerima (auto-deteksi):
  1. file respons JSON /route  -> pakai polyline_b64, polyline, atau points
  2. string base64             -> isi field polyline_b64
  3. string polyline mentah    -> tempelan dari JSON yang backslash-nya dobel
                                  ('\\\\' dikembalikan jadi '\\' otomatis)
  stdin juga bisa (pakai '-' sebagai path)

Pakai:
    scripts/polyline-decode.py r.json                 # ringkasan
    scripts/polyline-decode.py r.json > r.geojson     # GeoJSON
    scripts/polyline-decode.py --points r.json        # cetak semua titik

Tes cepat: jumlah titik harus sama dengan points_n dari server.
"""
import base64
import binascii
import json
import sys

from polyline_decode import decode_polyline, from_json


def main():
    args = [a for a in sys.argv[1:] if a != "--points"]
    show_points = "--points" in sys.argv
    src = args[0] if args else "-"
    raw = sys.stdin.read() if src == "-" else open(src).read()
    raw = raw.strip()

    if raw.startswith("{"):
        d = json.loads(raw)
        if "error" in d:
            raise SystemExit(f"server error: {d['error']}")
        pts, how = from_json(d)
        meta = d
    else:
        meta, pts, how = {}, None, None
        # coba base64 dulu (karakter base64 jauh lebih sempit dari polyline)
        try:
            dec = base64.b64decode(raw, validate=True).decode()
            if dec and all(ord(c) >= 63 for c in dec):
                pts, how = decode_polyline(dec), "base64"
        except (binascii.Error, UnicodeDecodeError, ValueError):
            pass
        if pts is None:
            if "\\\\" in raw:
                print(f"catatan: {raw.count(chr(92)*2)} backslash dobel diperbaiki", file=sys.stderr)
                raw = raw.replace("\\\\", "\\")
            pts, how = decode_polyline(raw), "polyline mentah"

    lats = [p[0] for p in pts]
    lons = [p[1] for p in pts]
    print(f"sumber     : {how}", file=sys.stderr)
    print(f"titik      : {len(pts)}", file=sys.stderr)
    if meta.get("points_n") is not None:
        cocok = "COCOK" if len(pts) == meta["points_n"] else "TIDAK COCOK: string rusak"
        print(f"points_n   : {meta['points_n']} ({cocok})", file=sys.stderr)
    print(f"bbox       : [{min(lats):.6f}, {min(lons):.6f}, {max(lats):.6f}, {max(lons):.6f}]", file=sys.stderr)
    print(f"awal/akhir : {pts[0]} -> {pts[-1]}", file=sys.stderr)
    if meta.get("to"):
        print(f"echo to    : {meta['to']}", file=sys.stderr)

    if show_points:
        json.dump(pts, sys.stdout, indent=0)
        print()
        return

    json.dump(
        {
            "type": "FeatureCollection",
            "features": [
                {
                    "type": "Feature",
                    "properties": {"kind": "route", "distance_m": meta.get("distance_m")},
                    "geometry": {"type": "LineString", "coordinates": [[p[1], p[0]] for p in pts]},
                }
            ],
        },
        sys.stdout,
        indent=1,
    )


if __name__ == "__main__":
    main()
