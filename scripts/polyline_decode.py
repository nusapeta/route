"""Dekoder polyline nusapeta-route (precision 1e-5), satu implementasi untuk
scripts/polyline-decode.py dan scripts/route2geojson.py.

Termasuk deteksi string terpotong dan pemilihan field JSON: polyline_b64,
polyline, lalu points.
"""
import base64


def decode_polyline(s):
    pts, i, lat, lng = [], 0, 0, 0
    while i < len(s):
        vals = []
        for _ in range(2):
            shift = result = 0
            while True:
                if i >= len(s):
                    raise ValueError(
                        f"polyline terpotong di karakter {i}/{len(s)} - "
                        "kemungkinan backslash ter-escape saat copy-paste"
                    )
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


def from_json(d):
    """Titik + label sumber dari satu objek respons /route."""
    if d.get("polyline_b64"):
        return decode_polyline(base64.b64decode(d["polyline_b64"]).decode()), "json:polyline_b64"
    if d.get("polyline"):
        return decode_polyline(d["polyline"]), "json:polyline"
    if d.get("points"):
        return [list(p) for p in d["points"]], "json:points"
    raise SystemExit("respons tidak punya points / polyline / polyline_b64")
