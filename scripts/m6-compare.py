#!/usr/bin/env python3
"""M6 / E1: routeid vs OSRM pada pasangan yang identik, dijalankan bergantian.

Untuk tiap pasangan di `--pairs` (kolom from_lat,from_lon,to_lat,to_lon):
  1. tanya routeid  (/route)   -> distance_m, duration_s, wall
  2. tanya OSRM     (/route/v1/driving) -> distance, duration, wall
Keduanya diukur di mesin yang sama, satu per satu (bukan paralel), supaya angka
latency bisa dibandingkan. OSRM memakai urutan lon,lat.

Contoh:
    scripts/m6-osrm.sh serve data/java-latest.osrm &
    bin/rid-server -graph data/java.graph -addr 127.0.0.1:8080 -rate 0 -conc 1 &
    scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
        --routeid http://127.0.0.1:8080 --osrm http://127.0.0.1:5000 \
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


def ask_routeid(hp, f, t, timeout):
    path = f"/route?from={f[0]},{f[1]}&to={t[0]},{t[1]}"
    st, ms, body = safe_get(hp, path, timeout)
    if st != 200:
        return {"ms": ms, "err": f"http {st}: {body[:80].decode(errors='replace')}"}
    j = json.loads(body)
    return {"ms": ms, "dist": j.get("distance_m"), "dur": j.get("duration_s"),
            "relaxed": bool(j.get("relaxed"))}


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
    return {"ms": ms, "dist": r["distance"], "dur": r["duration"]}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pairs", required=True)
    ap.add_argument("--routeid", default="http://127.0.0.1:8080")
    ap.add_argument("--osrm", default="http://127.0.0.1:5000")
    ap.add_argument("--csv", default="")
    ap.add_argument("--limit", type=int, default=0, help="0 = semua pasangan")
    ap.add_argument("--timeout", type=float, default=120.0)
    a = ap.parse_args()

    rid, osm = conn(a.routeid), conn(a.osrm)

    rows = list(csv.DictReader(open(a.pairs)))
    if a.limit:
        rows = rows[: a.limit]

    out = open(a.csv, "w", newline="") if a.csv else None
    w = csv.writer(out) if out else None
    if w:
        w.writerow(["bucket", "ride_ms", "osrm_ms", "ride_dist", "osrm_dist",
                    "ride_dur", "osrm_dur", "dist_delta_pct", "dur_ratio",
                    "from_lat", "from_lon", "to_lat", "to_lon", "relaxed", "err"])

    by = {}
    for i, r in enumerate(rows, 1):
        bucket = r["bucket"]
        f = (float(r["from_lat"]), float(r["from_lon"]))
        t = (float(r["to_lat"]), float(r["to_lon"]))
        # bergantian: osrm dulu di pasangan genap, routeid dulu di ganjil
        if i % 2:
            R = ask_routeid(rid, f, t, a.timeout)
            O = ask_osrm(osm, f, t, a.timeout)
        else:
            O = ask_osrm(osm, f, t, a.timeout)
            R = ask_routeid(rid, f, t, a.timeout)

        err = R.get("err") or O.get("err")
        dd = dr = None
        if not err and R.get("dist") and O.get("dist"):
            dd = (R["dist"] - O["dist"]) / O["dist"] * 100
            if O.get("dur"):
                dr = R["dur"] / O["dur"]
        if w:
            w.writerow([bucket, f"{R.get('ms', 0):.3f}", f"{O.get('ms', 0):.3f}",
                        R.get("dist"), O.get("dist"), R.get("dur"), O.get("dur"),
                        "" if dd is None else f"{dd:.2f}",
                        "" if dr is None else f"{dr:.4f}",
                        f[0], f[1], t[0], t[1], int(R.get("relaxed", False)), err or ""])

        b = by.setdefault(bucket, {"n": 0, "fail": 0, "ride": [], "osrm": [],
                                   "dd": [], "dr": [], "gt10": 0})
        if err:
            b["fail"] += 1
            continue
        b["n"] += 1
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

    print(f"{'bucket':<10}{'n':>5}{'fail':>6}{'ride_p50':>10}{'ride_p95':>10}"
          f"{'osrm_p50':>10}{'osrm_p95':>10}{'dist_delta%':>13}{'>10%':>7}{'dur_ratio r/o':>15}")
    for bucket, b in by.items():
        if not b["n"]:
            print(f"{bucket:<10}{0:>5}{b['fail']:>6}")
            continue
        gt = 100.0 * b["gt10"] / len(b["dd"]) if b["dd"] else 0.0
        print(f"{bucket:<10}{b['n']:>5}{b['fail']:>6}"
              f"{pct(b['ride'], 0.5):>10.1f}{pct(b['ride'], 0.95):>10.1f}"
              f"{pct(b['osrm'], 0.5):>10.1f}{pct(b['osrm'], 0.95):>10.1f}"
              f"{statistics.median(b['dd']) if b['dd'] else float('nan'):>13.2f}"
              f"{gt:>7.1f}"
              f"{statistics.median(b['dr']) if b['dr'] else float('nan'):>15.3f}")
    print("\nride = routeid, osrm = OSRM MLD. dist_delta% = (ride-osrm)/osrm.")
    print("Durasi tidak sebanding: routeid memakai tabel kecepatan kelas jalan,")
    print("OSRM car.lua memakai maxspeed. Yang bermakna: jarak (akurasi relatif)")
    print("dan latency/RSS (biaya).")


if __name__ == "__main__":
    main()
