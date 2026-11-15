#!/usr/bin/env python3
"""M6 / E1: nusapeta-route vs OSRM pada pasangan yang identik, dijalankan bergantian.

Untuk tiap pasangan di `--pairs` (kolom from_lat,from_lon,to_lat,to_lon):
  1. tanya nusapeta-route  (/route)   -> distance_m, duration_s, wall
  2. tanya OSRM     (/route/v1/driving) -> distance, duration, wall
Keduanya diukur di mesin yang sama, satu per satu (bukan paralel), supaya angka
latency bisa dibandingkan. OSRM memakai urutan lon,lat.

Contoh:
    scripts/m6-osrm.sh serve data/java-latest.osrm &
    bin/nusa-serve -graph data/java.graph -addr 127.0.0.1:8080 -rate 0 -conc 1 &
    scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
        --route http://127.0.0.1:8080 --osrm http://127.0.0.1:5000 \
        --csv paper/results/m6-osrm-vs-routeid.csv

Keluaran: tabel per bucket (p50/p95 latency masing-masing, median selisih jarak,
persentase pasangan dengan selisih >10%) + CSV mentah per pasangan.
"""
import argparse
import csv
import http.client
import json
import statistics
import sys
import time
from urllib.parse import urlparse


def pct(vals, q):
    if not vals:
        return float("nan")
    v = sorted(vals)
    i = min(len(v) - 1, int(q * (len(v) - 1) + 0.5))
    return v[i]


def haversine_m(a, b):
    """Jarak meter antara dua [lat, lon]."""
    from math import asin, cos, radians, sin, sqrt
    lat1, lon1 = map(radians, a)
    lat2, lon2 = map(radians, b)
    h = sin((lat2 - lat1) / 2) ** 2 + cos(lat1) * cos(lat2) * sin((lon2 - lon1) / 2) ** 2
    return 6371000.0 * 2 * asin(sqrt(h))


def conn(base):
    u = urlparse(base)
    return u.hostname, u.port or (443 if u.scheme == "https" else 80)


def get(host_port, path, timeout):
    c = http.client.HTTPConnection(host_port[0], host_port[1], timeout=timeout)
    t0 = time.perf_counter()
    try:
        c.request("GET", path)
        r = c.getresponse()
        body = r.read()
        ms = (time.perf_counter() - t0) * 1000
        return r.status, ms, body
    finally:
        c.close()


def safe_get(hp, path, timeout):
    """Like get() but turns a dead/refused server into a recorded error instead of
    a traceback: a missing OSRM should show up as a column of failures, not kill
    the run halfway."""
    try:
        return get(hp, path, timeout)
    except (OSError, http.client.HTTPException) as e:
        return 0, float("nan"), str(e).encode()


def ask_route(hp, f, t, timeout):
    path = f"/route?from={f[0]},{f[1]}&to={t[0]},{t[1]}"
    st, ms, body = safe_get(hp, path, timeout)
    if st != 200:
        return {"ms": ms, "err": f"http {st}: {body[:80].decode(errors='replace')}"}
    j = json.loads(body)
    return {"ms": ms, "dist": j.get("distance_m"), "dur": j.get("duration_s"),
            "relaxed": bool(j.get("relaxed")),
            "snap_f": j.get("snap_from_m"), "snap_t": j.get("snap_to_m"),
            "snap_f_ll": j.get("snap_from"), "snap_t_ll": j.get("snap_to")}


def ask_osrm(hp, f, t, timeout):
    path = (f"/route/v1/driving/{f[1]},{f[0]};{t[1]},{t[0]}"
            "?overview=false&alternatives=false&steps=false")
    st, ms, body = safe_get(hp, path, timeout)
    if st != 200:
        return {"ms": ms, "err": f"http {st}: {body[:80].decode(errors='replace')}"}
    j = json.loads(body)
    if j.get("code") != "Ok" or not j.get("routes"):
        return {"ms": ms, "err": str(j.get("code"))}
    r = j["routes"][0]
    # Waypoint distance = jarak dari koordinat input ke titik snap. Tanpa ini,
    # pasangan yang targetnya jatuh di laut/hutan terlihat "berhasil" padahal
    # kedua engine menempel ke jalan yang berbeda (bisa beda 8 km).
    wp = j.get("waypoints") or []
    return {"ms": ms, "dist": r["distance"], "dur": r["duration"],
            "snap_f": wp[0].get("distance") if len(wp) > 0 else None,
            "snap_t": wp[1].get("distance") if len(wp) > 1 else None,
            "snap_f_ll": wp[0].get("location")[::-1] if len(wp) > 0 else None,
            "snap_t_ll": wp[1].get("location")[::-1] if len(wp) > 1 else None}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pairs", required=True)
    # --routeid is the pre-rename name; paper/RESULTS.md and paper/COMPARISON.md
    # record commands with it, so it stays accepted for reproduction.
    ap.add_argument("--route", "--routeid", dest="route",
                    default="http://127.0.0.1:8080")
    ap.add_argument("--osrm", default="http://127.0.0.1:5000")
    ap.add_argument("--csv", default="")
    ap.add_argument("--limit", type=int, default=0, help="0 = semua pasangan")
    ap.add_argument("--timeout", type=float, default=120.0)
    ap.add_argument("--max-snap", type=float, default=0.0,
                    help="hanya hitung pasangan yang keempat titik snap <= N meter "
                         "(0 = tanpa filter; error tetap dihitung terpisah)")
    ap.add_argument("--max-gap", type=float, default=0.0,
                    help="hanya hitung pasangan yang titik snap nusapeta-route dan OSRM "
                         "berjarak <= N meter di kedua ujung (0 = tanpa filter)")
    a = ap.parse_args()

    rid, osm = conn(a.route), conn(a.osrm)

    rows = list(csv.DictReader(open(a.pairs)))
    if a.limit:
        rows = rows[: a.limit]

    out = open(a.csv, "w", newline="") if a.csv else None
    w = csv.writer(out) if out else None
    if w:
        w.writerow(["bucket", "ride_ms", "osrm_ms", "ride_dist", "osrm_dist",
                    "ride_dur", "osrm_dur", "dist_delta_pct", "dur_ratio",
                    "ride_snap_f", "ride_snap_t", "osrm_snap_f", "osrm_snap_t",
                    "ride_snap_from_lat", "ride_snap_from_lon",
                    "ride_snap_to_lat", "ride_snap_to_lon",
                    "osrm_snap_from_lat", "osrm_snap_from_lon",
                    "osrm_snap_to_lat", "osrm_snap_to_lon",
                    "snap_gap_f", "snap_gap_t",
                    "from_lat", "from_lon", "to_lat", "to_lon", "relaxed",
                    "ride_err", "osrm_err"])

    by = {}
    for i, r in enumerate(rows, 1):
        bucket = r["bucket"]
        f = (float(r["from_lat"]), float(r["from_lon"]))
        t = (float(r["to_lat"]), float(r["to_lon"]))
        # bergantian: osrm dulu di pasangan genap, nusapeta-route dulu di ganjil
        if i % 2:
            R = ask_route(rid, f, t, a.timeout)
            O = ask_osrm(osm, f, t, a.timeout)
        else:
            O = ask_osrm(osm, f, t, a.timeout)
            R = ask_route(rid, f, t, a.timeout)

        err = R.get("err") or O.get("err")
        gap_f = gap_t = None
        if all(R.get(k) and O.get(k) for k in ("snap_f_ll", "snap_t_ll")):
            gap_f = haversine_m(R["snap_f_ll"], O["snap_f_ll"])
            gap_t = haversine_m(R["snap_t_ll"], O["snap_t_ll"])
        dd = dr = None
        if not err and R.get("dist") and O.get("dist"):
            dd = (R["dist"] - O["dist"]) / O["dist"] * 100
            if O.get("dur"):
                dr = R["dur"] / O["dur"]
        if w:
            rfl = R.get("snap_f_ll") or [None, None]
            rtl = R.get("snap_t_ll") or [None, None]
            ofl = O.get("snap_f_ll") or [None, None]
            otl = O.get("snap_t_ll") or [None, None]
            w.writerow([bucket, f"{R.get('ms', 0):.3f}", f"{O.get('ms', 0):.3f}",
                        R.get("dist"), O.get("dist"), R.get("dur"), O.get("dur"),
                        "" if dd is None else f"{dd:.2f}",
                        "" if dr is None else f"{dr:.4f}",
                        R.get("snap_f"), R.get("snap_t"),
                        O.get("snap_f"), O.get("snap_t"),
                        rfl[0], rfl[1], rtl[0], rtl[1],
                        ofl[0], ofl[1], otl[0], otl[1],
                        "" if gap_f is None else f"{gap_f:.6f}",
                        "" if gap_t is None else f"{gap_t:.6f}",
                        f[0], f[1], t[0], t[1], int(R.get("relaxed", False)),
                        R.get("err") or "", O.get("err") or ""])

        b = by.setdefault(bucket, {"fail": 0, "excl": 0, "ride": [], "osrm": [],
                                   "dd": [], "dr": [], "gt10": 0})
        if err:
            b["fail"] += 1
            continue
        snaps = [R.get("snap_f"), R.get("snap_t"),
                 O.get("snap_f"), O.get("snap_t")]
        if a.max_snap > 0 and (any(s is None for s in snaps)
                               or any(s > a.max_snap for s in snaps)):
            b["excl"] += 1
            continue
        if a.max_gap > 0 and (gap_f is None or gap_t is None
                              or gap_f > a.max_gap or gap_t > a.max_gap):
            b["excl"] += 1
            continue
        b["ride"].append(R["ms"])
        b["osrm"].append(O["ms"])
        if dd is not None:
            b["dd"].append(dd)
            b["dr"].append(dr)
            if abs(dd) > 10:
                b["gt10"] += 1
        if i % 25 == 0:
            print(f"  {i}/{len(rows)}", file=sys.stderr, flush=True)

    if out:
        out.close()

    print(f"{'bucket':<10}{'n':>5}{'fail':>6}{'snap>N':>7}{'ride_p50':>10}{'ride_p95':>10}"
          f"{'osrm_p50':>10}{'osrm_p95':>10}{'dist_delta%':>13}{'>10%':>7}{'dur_ratio r/o':>15}")
    for bucket, b in by.items():
        n = len(b["ride"])
        if not n:
            print(f"{bucket:<10}{0:>5}{b['fail']:>6}{b['excl']:>7}")
            continue
        gt = 100.0 * b["gt10"] / len(b["dd"]) if b["dd"] else 0.0
        print(f"{bucket:<10}{n:>5}{b['fail']:>6}{b['excl']:>7}"
              f"{pct(b['ride'], 0.5):>10.1f}{pct(b['ride'], 0.95):>10.1f}"
              f"{pct(b['osrm'], 0.5):>10.1f}{pct(b['osrm'], 0.95):>10.1f}"
              f"{statistics.median(b['dd']) if b['dd'] else float('nan'):>13.2f}"
              f"{gt:>7.1f}"
              f"{statistics.median(b['dr']) if b['dr'] else float('nan'):>15.3f}")
    print("\nride = nusapeta-route, osrm = OSRM MLD. dist_delta% = (ride-osrm)/osrm.")
    print("Durasi tidak sebanding: nusapeta-route memakai tabel kecepatan kelas jalan,")
    print("OSRM car.lua memakai maxspeed. Yang bermakna: jarak (akurasi relatif)")
    print("dan latency/RSS (biaya).")


if __name__ == "__main__":
    main()
