#!/usr/bin/env python3
"""A/B the car (mobil) and motorcycle (motor) profiles on fixed pairs.

Both upstreams get the same coordinates and ``format=polyline``. The CSV keeps
only route-derived fields, so two runs over the same data are byte-identical;
wall latency is intentionally absent (use nusa-bench for performance). Pace is
4.2 s by default, just under the deployed 15 requests/minute limiter.

Contoh direct upstream:
    scripts/motor-ab.py --pairs paper/results/pairs-java.csv \
        --mobil http://127.0.0.1:18080 --motor http://127.0.0.1:18081 \
        --csv paper/results/motor-ab.csv --summary paper/results/motor-ab.txt

Contoh lewat gateway (profil dipilih di query string):
    scripts/motor-ab.py --pairs paper/results/pairs-java.csv \
        --mobil 'http://192.168.1.219:8080?profile=mobil' \
        --motor 'http://192.168.1.219:8080?profile=motor' \
        --pause 4.2 --csv paper/results/motor-ab.csv

Pasangan yang jaraknya >100 km akan 422; itu dicatat sebagai error, bukan
dibuang, supaya batas produk tetap terlihat di data.
"""
import argparse
import csv
import hashlib
import http.client
import json
import statistics
import sys
import time
from urllib.parse import parse_qsl, urlencode, urlparse


def pct(vals, q):
    if not vals:
        return float("nan")
    v = sorted(vals)
    i = min(len(v) - 1, int(q * (len(v) - 1) + 0.5))
    return v[i]


def conn(base):
    u = urlparse(base)
    port = u.port or (443 if u.scheme == "https" else 80)
    if u.scheme == "https":
        return http.client.HTTPSConnection(u.hostname, port), u
    return http.client.HTTPConnection(u.hostname, port), u


def route_path(u, f, t):
    p = u.path.rstrip("/")
    if not p.endswith("/route"):
        p += "/route"
    params = parse_qsl(u.query, keep_blank_values=True)
    params += [
        ("from", f"{f[0]},{f[1]}"),
        ("to", f"{t[0]},{t[1]}"),
        ("format", "polyline"),
    ]
    return p + "?" + urlencode(params)


def safe_get(c, path, timeout):
    # HTTPConnection only applies timeout at connect time; a reused keep-alive
    # socket keeps the old value, so set it on the live socket too.
    c.timeout = timeout
    if c.sock is not None:
        c.sock.settimeout(timeout)
    try:
        c.request("GET", path)
        r = c.getresponse()
        body = r.read()
        return r.status, body
    except (OSError, http.client.HTTPException) as e:
        return 0, str(e).encode()


def ask(c, u, f, t, timeout):
    st, body = safe_get(c, route_path(u, f, t), timeout)
    if st != 200:
        return {"err": f"http {st}: {body[:120].decode(errors='replace')}"}
    try:
        j = json.loads(body)
    except ValueError as e:
        return {"err": f"bad json: {e}"}
    poly = j.get("polyline")
    if poly is not None:
        geom = poly.encode()
    else:
        geom = json.dumps(j.get("points") or [], separators=(",", ":")).encode()
    return {
        "dist": j.get("distance_m"),
        "dur": j.get("duration_s"),
        "region": j.get("region"),
        "snap_f": j.get("snap_from_m"),
        "snap_t": j.get("snap_to_m"),
        "points": j.get("points_n"),
        "relaxed": bool(j.get("relaxed")),
        "hash": hashlib.sha256(geom).hexdigest(),
    }


def num(v, fmt):
    return "" if v is None else format(v, fmt)


def region_key(name):
    """Graph names carry the profile suffix (-motor); geography is the same."""
    if not name:
        return ""
    return name[:-6] if name.endswith("-motor") else name


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pairs", required=True)
    ap.add_argument("--mobil", default="http://127.0.0.1:18080")
    ap.add_argument("--motor", default="http://127.0.0.1:18081")
    ap.add_argument("--csv", default="")
    ap.add_argument("--summary", default="")
    ap.add_argument("--limit", type=int, default=0, help="0 = semua pasangan")
    ap.add_argument("--timeout", type=float, default=120.0)
    ap.add_argument("--pause", type=float, default=4.2,
                    help="detik antar pasangan; 4.2 < 15 req/menit (0 = tanpa jeda)")
    a = ap.parse_args()

    mobil_conn, mobil_u = conn(a.mobil)
    motor_conn, motor_u = conn(a.motor)

    rows = list(csv.DictReader(open(a.pairs)))
    if a.limit:
        rows = rows[: a.limit]

    out = open(a.csv, "w", newline="") if a.csv else None
    w = csv.writer(out) if out else None
    if w:
        w.writerow([
            "bucket", "from_lat", "from_lon", "to_lat", "to_lon",
            "mobil_dist", "mobil_dur", "mobil_region", "mobil_snap_f",
            "mobil_snap_t", "mobil_points", "mobil_relaxed", "mobil_hash",
            "mobil_err",
            "motor_dist", "motor_dur", "motor_region", "motor_snap_f",
            "motor_snap_t", "motor_points", "motor_relaxed", "motor_hash",
            "motor_err",
            "dist_delta_pct", "dur_ratio", "geom_same", "region_same",
        ])

    by = {}
    for i, r in enumerate(rows, 1):
        bucket = r["bucket"]
        f = (float(r["from_lat"]), float(r["from_lon"]))
        t = (float(r["to_lat"]), float(r["to_lon"]))
        M = ask(mobil_conn, mobil_u, f, t, a.timeout)
        K = ask(motor_conn, motor_u, f, t, a.timeout)
        if a.pause and i < len(rows):
            time.sleep(a.pause)

        err = bool(M.get("err") or K.get("err"))
        dd = dr = None
        same = False
        region_same = False
        if not err:
            if M.get("dist") and K.get("dist") is not None:
                dd = (K["dist"] - M["dist"]) / M["dist"] * 100
            if M.get("dur"):
                dr = K["dur"] / M["dur"]
            same = M.get("hash") == K.get("hash")
            region_same = region_key(M.get("region")) == region_key(K.get("region"))

        if w:
            w.writerow([
                bucket, f[0], f[1], t[0], t[1],
                M.get("dist"), num(M.get("dur"), ".1f"), M.get("region"),
                M.get("snap_f"), M.get("snap_t"), M.get("points"),
                int(M.get("relaxed", False)), M.get("hash"), M.get("err") or "",
                K.get("dist"), num(K.get("dur"), ".1f"), K.get("region"),
                K.get("snap_f"), K.get("snap_t"), K.get("points"),
                int(K.get("relaxed", False)), K.get("hash"), K.get("err") or "",
                num(dd, ".3f"), num(dr, ".4f"),
                "" if err else int(same), "" if err else int(region_same),
            ])

        b = by.setdefault(bucket, {
            "n": 0, "fail": 0, "mobil_fail": 0, "motor_fail": 0,
            "m_dist": [], "k_dist": [], "dd": [], "dr": [],
            "same": 0, "region_bad": 0, "relaxed": 0,
        })
        if err:
            b["fail"] += 1
            b["mobil_fail"] += int(bool(M.get("err")))
            b["motor_fail"] += int(bool(K.get("err")))
            continue
        b["n"] += 1
        b["m_dist"].append(M["dist"])
        b["k_dist"].append(K["dist"])
        if dd is not None:
            b["dd"].append(dd)
        if dr is not None:
            b["dr"].append(dr)
        b["same"] += int(same)
        b["region_bad"] += int(not region_same)
        b["relaxed"] += int(bool(M.get("relaxed")) or bool(K.get("relaxed")))

        if i % 25 == 0:
            print(f"  {i}/{len(rows)}", file=sys.stderr, flush=True)

    if out:
        out.close()

    lines = []
    lines.append(
        f"{'bucket':<10}{'n':>5}{'fail':>6}{'m_fail':>7}{'k_fail':>7}"
        f"{'m_dist':>9}{'k_dist':>9}{'dd_med%':>10}{'|dd|p95%':>10}"
        f"{'dur_med':>9}{'dur_p95':>9}{'same':>7}{'reg_bad':>9}{'relax':>7}"
    )
    total = {"n": 0, "fail": 0, "mobil_fail": 0, "motor_fail": 0,
             "m_dist": [], "k_dist": [], "dd": [], "dr": [],
             "same": 0, "region_bad": 0, "relaxed": 0}
    for bucket, b in by.items():
        for k in total:
            total[k] += b[k]
        lines.append(summary_row(bucket, b))
    lines.append(summary_row("ALL", total))
    text = "\n".join(lines) + "\n"
    sys.stdout.write(text)
    if a.summary:
        with open(a.summary, "w") as fh:
            fh.write(text)


def summary_row(label, b):
    n = b["n"]
    dd = b["dd"]
    dr = b["dr"]
    return (
        f"{label:<10}{n:>5}{b['fail']:>6}{b['mobil_fail']:>7}{b['motor_fail']:>7}"
        f"{statistics.median(b['m_dist']) if b['m_dist'] else float('nan'):>9.0f}"
        f"{statistics.median(b['k_dist']) if b['k_dist'] else float('nan'):>9.0f}"
        f"{statistics.median(dd) if dd else float('nan'):>10.2f}"
        f"{pct([abs(x) for x in dd], 0.95):>10.2f}"
        f"{statistics.median(dr) if dr else float('nan'):>9.3f}"
        f"{pct([abs(x) for x in dr], 0.95):>9.3f}"
        f"{b['same']:>7}{b['region_bad']:>9}{b['relaxed']:>7}"
    )


if __name__ == "__main__":
    main()
